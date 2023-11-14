package gol

import (
	"fmt"
	"net/rpc"
	"os"
	"strconv"
	"time"

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

// build 接收长度和宽度并生成一个指定长度x宽度的2D矩阵
func build(height, width int) [][]uint8 {
	newMatrix := make([][]uint8, height)
	for i := range newMatrix {
		newMatrix[i] = make([]uint8, width)
	}
	return newMatrix
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {
	server, err := rpc.Dial("tcp", "localhost:8080")
	dialError(err, c)

	turn := 0
	c.ioCommand <- ioInput
	c.ioFilename <- strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.ImageWidth)

	world := build(p.ImageHeight, p.ImageWidth)
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			value := <-c.ioInput
			world[y][x] = value
		}
	}

	golBoard := stubs.GolBoard{World: world, Width: p.ImageWidth, Height: p.ImageHeight}
	var res stubs.NextStateResponse
	go func() {
		err = server.Call("Server.RunGol", req, &res)
		dialError(err, c)
		fmt.Println(res.GolBoard)
	}()

	time.Sleep(30 * time.Second)
	// TODO: Report the final state using FinalTurnCompleteEvent.

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
