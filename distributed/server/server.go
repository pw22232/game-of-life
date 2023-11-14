package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"uk.ac.bris.cs/gameoflife/stubs"
)

type Server struct {
}

func handleError(err error) {
	fmt.Println("Error:", err)
}

func (s *Server) Test(req stubs.TestReq, res *stubs.TestRes) (err error) {
	res.Value = req.Value + 1
	return
}

func main() {
	portPtr := flag.String("port", ":8080", "port to listen on")
	flag.Parse()

	ln, err := net.Listen("tcp", *portPtr)

	if err != nil {
		handleError(err)
		return
	}
	defer ln.Close()
	rpc.Register(new(Server))
	rpc.Accept(ln)
}
