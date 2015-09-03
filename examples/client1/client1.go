package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/dustywilson/lmifeedr"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var serverAddr = flag.String("s", "127.0.0.1:23232", "Server address and port")

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	conn, err := grpc.Dial(*serverAddr, grpc.WithInsecure())
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	defer conn.Close()

	c := lmifeedr.NewLMIFeedrClient(conn)
	{
		computer, err := c.GetComputer(context.Background(), &lmifeedr.ComputerRequest{
			NameMatch: "MUSHROOM",
		})
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Printf("COMPUTER:[%+v]\n", computer)
		}
	}
	{
		computer, err := c.GetComputer(context.Background(), &lmifeedr.ComputerRequest{
			HostID: 1231231234,
		})
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Printf("COMPUTER:[%+v]\n", computer)
		}
	}
	{
		computer, err := c.GetComputer(context.Background(), &lmifeedr.ComputerRequest{
			NameMatch: "FRED",
		})
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Printf("COMPUTER:[%+v]\n", computer)
		}
	}
	{
		computer, err := c.GetComputer(context.Background(), &lmifeedr.ComputerRequest{
			IpMatch: "216.0.0.0/8",
		})
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Printf("COMPUTER:[%+v]\n", computer)
		}
	}
	{
		// this one should fail as malformed.
		computer, err := c.GetComputer(context.Background(), &lmifeedr.ComputerRequest{
			IpMatch: "999/8",
		})
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Printf("COMPUTER:[%+v]\n", computer)
		}
	}
	{
		computer, err := c.GetComputer(context.Background(), &lmifeedr.ComputerRequest{
			IpMatch: "1.2.3.4",
		})
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Printf("COMPUTER:[%+v]\n", computer)
		}
	}
}
