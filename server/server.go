package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/dustywilson/lmifeedr"
	"github.com/scjalliance/logmein"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"gopkg.in/gcfg.v1"
)

type config struct {
	RSS struct {
		ProfileID uint64 `gcfg:"profile"`
		Key       string `gcfg:"key"`
	} `gcfg:"rss"`
}

var configFile = flag.String("c", "config.conf", "Config file")

// ErrNoMatch is an error that means that the requested query returned zero results
var ErrNoMatch = errors.New("No match for query")

type lmiServer struct {
	logmein              *logmein.LMI
	regexpSet            map[string]*regexp.Regexp
	hostIDSubscribers    map[uint64][]chan<- *logmein.Computer
	nameMatchSubscribers map[*regexp.Regexp][]chan<- *logmein.Computer
	ipMatchSubscribers   map[string][]chan<- *logmein.Computer
	sync.RWMutex
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	cfg := new(config)
	err := gcfg.ReadFileInto(cfg, *configFile)
	if err != nil {
		fmt.Println("Config Error: ", err)
		os.Exit(1)
	}

	l, err := net.Listen("tcp", "127.0.0.1:23232")
	if err != nil {
		panic(err)
	}

	lmi := logmein.NewLMI(cfg.RSS.ProfileID, cfg.RSS.Key)

	svr := &lmiServer{
		logmein:              lmi,
		regexpSet:            make(map[string]*regexp.Regexp),
		hostIDSubscribers:    make(map[uint64][]chan<- *logmein.Computer),
		nameMatchSubscribers: make(map[*regexp.Regexp][]chan<- *logmein.Computer),
		ipMatchSubscribers:   make(map[string][]chan<- *logmein.Computer),
	}

	grpcServer := grpc.NewServer()
	lmifeedr.RegisterLMIFeedrServer(grpcServer, svr)
	go func() { panic(grpcServer.Serve(l)) }()

	recv := make(chan *logmein.Computer)
	stop := make(chan struct{})
	go lmi.Watch(recv, stop, false)

	for {
		svr.processComputer(<-recv)
	}
}

func (svr *lmiServer) processComputer(computer *logmein.Computer) {
	svr.RLock()
	defer svr.RUnlock()

	fmt.Printf("PC:[%+v]\n", computer)

	// receivedThisComputer map should help prevent duplicate computers from crossing the wire
	receivedThisComputer := make(map[chan<- *logmein.Computer]bool)

	if subs, ok := svr.hostIDSubscribers[computer.HostID()]; ok {
		for _, sub := range subs {
			if _, ok := receivedThisComputer[sub]; !ok {
				sub <- computer
				receivedThisComputer[sub] = true
			}
		}
	} else if subs, ok := svr.hostIDSubscribers[computer.OldHostID()]; ok {
		// presently I know of no situation where we ever see an "OldHostID != HostID" except at creation where OldHostID == 0
		for _, sub := range subs {
			if _, ok := receivedThisComputer[sub]; !ok {
				sub <- computer
				receivedThisComputer[sub] = true
			}
		}
	}

	for _, re := range svr.regexpSet {
		if subs, ok := svr.nameMatchSubscribers[re]; ok {
			if re.MatchString(computer.Name()) || re.MatchString(computer.OldName()) {
				for _, sub := range subs {
					if _, ok := receivedThisComputer[sub]; !ok {
						sub <- computer
						receivedThisComputer[sub] = true
					}
				}
			}
		}
	}

	for ipNetStr, subs := range svr.ipMatchSubscribers {
		_, ipNet, err := net.ParseCIDR(ipNetStr)
		if err == nil {
			if ipNet.Contains(computer.IPAddress()) || ipNet.Contains(computer.OldIPAddress()) {
				for _, sub := range subs {
					sub <- computer
				}
			}
		}
	}
}

func convertComputerToGRPC(c *logmein.Computer) *lmifeedr.Computer {
	v := &lmifeedr.Computer{
		HostID:    c.HostID(),
		OldHostID: c.OldHostID(),
		Name:      c.Name(),
		OldName:   c.OldName(),
		Status:    int32(c.Status()),
		OldStatus: int32(c.OldStatus()),
		ChangeSet: uint32(c.GetChangeSet()),
	}
	if c.IPAddress() != nil {
		v.Ip = c.IPAddress().String()
	}
	if c.OldIPAddress() != nil {
		v.OldIp = c.OldIPAddress().String()
	}
	return v
}

func (svr *lmiServer) registerWatchChan(req *lmifeedr.WatchRequest, firehose chan<- *logmein.Computer) error {
	// update this in sync with `unregisterWatchChan`

	svr.Lock()
	defer svr.Unlock()

	for _, id := range req.HostID {
		if _, ok := svr.hostIDSubscribers[id]; !ok {
			svr.hostIDSubscribers[id] = []chan<- *logmein.Computer{firehose}
		} else {
			svr.hostIDSubscribers[id] = append(svr.hostIDSubscribers[id], firehose)
		}
	}

	for _, nameMatch := range req.NameMatch {
		var re *regexp.Regexp
		if reCache, ok := svr.regexpSet[nameMatch]; ok {
			re = reCache
		} else {
			var err error
			re, err = regexp.Compile(nameMatch)
			if err != nil {
				return err
			}
			svr.regexpSet[nameMatch] = re
		}
		if _, ok := svr.nameMatchSubscribers[re]; !ok {
			svr.nameMatchSubscribers[re] = []chan<- *logmein.Computer{firehose}
		} else {
			svr.nameMatchSubscribers[re] = append(svr.nameMatchSubscribers[re], firehose)
		}
	}

	for _, ip := range req.IpMatch {
		ip = ensureIPIsCIDR(ip)
		_, _, err := net.ParseCIDR(ip)
		if err != nil {
			return err
		}
		if _, ok := svr.ipMatchSubscribers[ip]; !ok {
			svr.ipMatchSubscribers[ip] = []chan<- *logmein.Computer{firehose}
		} else {
			svr.ipMatchSubscribers[ip] = append(svr.ipMatchSubscribers[ip], firehose)
		}
	}

	return nil
}

func (svr *lmiServer) unregisterWatchChan(req *lmifeedr.WatchRequest, firehose chan<- *logmein.Computer) error {
	// update this in sync with `registerWatchChan`

	svr.Lock()
	defer svr.Unlock()

	for _, id := range req.HostID {
		if subs, ok := svr.hostIDSubscribers[id]; ok {
		UnsubHostIDLoop:
			for {
				for i, ch := range subs {
					if ch == firehose {
						subs, subs[len(subs)-1] = append(subs[:i], subs[i+1:]...), nil
						continue UnsubHostIDLoop
					}
				}
				break
			}
		}
	}

	for _, nameMatch := range req.NameMatch {
		if re, ok := svr.regexpSet[nameMatch]; ok {
			if subs, ok := svr.nameMatchSubscribers[re]; ok {
			UnsubNameMatchLoop:
				for {
					for i, ch := range subs {
						if ch == firehose {
							subs, subs[len(subs)-1] = append(subs[:i], subs[i+1:]...), nil
							continue UnsubNameMatchLoop
						}
					}
					break
				}
			}
		}
	}

	for _, ip := range req.IpMatch {
		ip = ensureIPIsCIDR(ip)
		if subs, ok := svr.ipMatchSubscribers[ip]; ok {
		UnsubIPMatchLoop:
			for {
				for i, ch := range subs {
					if ch == firehose {
						subs, subs[len(subs)-1] = append(subs[:i], subs[i+1:]...), nil
						continue UnsubIPMatchLoop
					}
				}
				break
			}
		}
	}

	close(firehose)

	return nil
}

// GetComputer returns the first match.  It's best to be used for looking up computers by ID or name where you expect exactly one result.
func (svr *lmiServer) GetComputer(c context.Context, req *lmifeedr.ComputerRequest) (*lmifeedr.Computer, error) {
	svr.RLock()
	defer svr.RUnlock()

	computers := svr.logmein.Computers()
	if req.HostID > 0 {
		if c, ok := computers[req.HostID]; ok {
			return convertComputerToGRPC(c), nil
		}
	}
	if len(req.NameMatch) > 0 {
		re, err := regexp.Compile(req.NameMatch)
		if err != nil {
			return nil, err
		}
		for _, c := range computers {
			if re.MatchString(c.Name()) {
				return convertComputerToGRPC(c), nil
			}
		}
	}
	if len(req.IpMatch) > 0 {
		_, ipNet, err := net.ParseCIDR(ensureIPIsCIDR(req.IpMatch))
		if err != nil {
			return nil, err
		}
		for _, c := range computers {
			if ipNet.Contains(c.IPAddress()) {
				return convertComputerToGRPC(c), nil
			}
		}
	}
	return nil, ErrNoMatch
}

// GetComputers returns a stream of all matching computers.
func (svr *lmiServer) GetComputers(req *lmifeedr.ComputerRequest, stream lmifeedr.LMIFeedr_GetComputersServer) error {
	svr.RLock()
	defer svr.RUnlock()

	returned := make(map[uint64]bool) // which computers have we already sent over the wire this time around?

	computers := svr.logmein.Computers()
	if req.HostID > 0 {
		if c, ok := computers[req.HostID]; ok {
			if !returned[c.HostID()] {
				if err := stream.Send(convertComputerToGRPC(c)); err != nil {
					return err
				}
				returned[c.HostID()] = true
			}
		}
	}
	if len(req.NameMatch) > 0 {
		re, err := regexp.Compile(req.NameMatch)
		if err != nil {
			return err
		}
		for _, c := range computers {
			if re.MatchString(c.Name()) {
				if !returned[c.HostID()] {
					if err := stream.Send(convertComputerToGRPC(c)); err != nil {
						return err
					}
					returned[c.HostID()] = true
				}
			}
		}
	}
	if len(req.IpMatch) > 0 {
		_, ipNet, err := net.ParseCIDR(ensureIPIsCIDR(req.IpMatch))
		if err != nil {
			return err
		}
		for _, c := range computers {
			if ipNet.Contains(c.IPAddress()) {
				if !returned[c.HostID()] {
					if err := stream.Send(convertComputerToGRPC(c)); err != nil {
						return err
					}
					returned[c.HostID()] = true
				}
			}
		}
	}
	return ErrNoMatch
}

// Watch returns a neverending stream of state changes, until the client disconnects.
func (svr *lmiServer) Watch(req *lmifeedr.WatchRequest, stream lmifeedr.LMIFeedr_WatchServer) error {
	firehose := make(chan *logmein.Computer)
	err := svr.registerWatchChan(req, firehose)
	if err != nil {
		return err
	}
	defer svr.unregisterWatchChan(req, firehose)
	for {
		water := <-firehose
		err = stream.Send(convertComputerToGRPC(water))
		if err != nil {
			return err
		}
	}
}

func ensureIPIsCIDR(ip string) string {
	if !strings.Contains(ip, "/") {
		ip = ip + "/32"
	}
	return ip
}
