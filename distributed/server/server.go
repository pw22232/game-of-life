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

func acceptConns(ln net.Listener, conns chan net.Conn) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			handleError(err)
			return
		}
		conns <- conn
	}
}

func test(req stubs.TestReq, res stubs.TestRes) {
	res.Value = req.Value + 1
	return
}

func main() {
	portPtr := flag.String("port", ":8080", "port to listen on")
	flag.Parse()

	ln, err := net.Listen("tcp", *portPtr)
	conns := make(chan net.Conn)

	if err != nil {
		handleError(err)
		return

	}
	defer ln.Close()
	go acceptConns(ln, conns)
	rpc.Register(new(Server))
	rpc.Accept(ln)

}
