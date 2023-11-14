package gol

import (
	"fmt"
	"net/rpc"
	"os"
	"uk.ac.bris.cs/gameoflife/stubs"
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
}

func dialError(err error, c distributorChannels) {
	if err != nil {
		fmt.Println(err)
		c.ioCommand <- ioCheckIdle
		<-c.ioIdle
		c.events <- StateChange{NewState: Quitting}
		close(c.events)
		os.Exit(1)
	}
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {
	server, err := rpc.Dial("tcp", "localhost:8080")
	dialError(err, c)

	res := stubs.TestRes{}
	err = server.Call("Server.Test", stubs.TestReq{Value: 3}, &res)
	dialError(err, c)
	fmt.Println(res.Value)
	turn := 0

	// TODO: Execute all turns of the Game of Life.

	// TODO: Report the final state using FinalTurnCompleteEvent.

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
