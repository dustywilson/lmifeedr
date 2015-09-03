package main

import (
	"flag"
	"fmt"
	"io"
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
		stream, err := c.GetComputers(context.Background(), &lmifeedr.ComputerRequest{
			NameMatch: `^SCJ\d+\b`,
		})
		if err != nil {
			fmt.Println("Error1: ", err)
		} else {
			defer stream.CloseSend()
			for {
				computer, err := stream.Recv()
				if err == io.EOF {
					log.Println("EOF")
					break
				}
				if err != nil {
					fmt.Println("Error2: ", err)
					break
				} else {
					fmt.Printf("COMPUTER:[%+v]\n", computer)
				}
			}
		}
	}
}
