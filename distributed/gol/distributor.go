package gol

import (
	"fmt"
	"net/rpc"
	"os"
	"strconv"
	"time"
	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
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

// findAliveCells 返回世界中所有存活的细胞
func findAliveCells(p Params, world [][]uint8) []util.Cell {
	var aliveCells []util.Cell
	for x := 0; x < p.ImageWidth; x++ {
		for y := 0; y < p.ImageHeight; y++ {
			if world[y][x] == 255 {
				aliveCells = append(aliveCells, util.Cell{X: x, Y: y})
			}
		}
	}
	return aliveCells
}
func outputPGM(c distributorChannels, p Params, turn int, world [][]uint8) {
	c.ioCommand <- ioOutput
	outFilename := strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.ImageWidth) + "x" + strconv.Itoa(turn)
	c.ioFilename <- outFilename
	// 输出世界，ioOutput通道会每次传递一个值，从世界的左上角到右下角

	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			c.ioOutput <- world[y][x]
		}
	}

	c.ioCommand <- ioCheckIdle
	<-c.ioIdle
	c.events <- ImageOutputComplete{CompletedTurns: turn, Filename: outFilename}
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels, keyPresses <-chan rune) {
	address := "localhost"
	port := "8080"

	server, err := rpc.Dial("tcp", address+":"+port)
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

	finalTurnFinish := make(chan bool)
	countFinish := make(chan bool)
	quit := make(chan bool)

	golBoard := stubs.GolBoard{World: world, Width: p.ImageWidth, Height: p.ImageHeight}
	req := stubs.RunGolRequest{GolBoard: golBoard, Turns: p.Turns, Threads: p.Threads}
	var countReq stubs.AliveCellsCountRequest
	var res stubs.RunGolResponse
	var countRes stubs.AliveCellsCountResponse
	var worldReq stubs.CurrentWorldRequest
	var worldRes stubs.CurrentWorldResponse
	ticker := time.NewTicker(2 * time.Second)
	// 调用服务器运行所有的回合
	go func() {
		err = server.Call("Server.RunGol", req, &res)
		if err != nil {
			finalTurnFinish <- true
		}
	}()

	// 调用服务器每隔两秒返回存活细胞数量
	go func() {
		for {
			<-ticker.C
			err = server.Call("Server.CountAliveCells", countReq, &countRes)
			dialError(err, c)
			countFinish <- true
		}
	}()

	// 按键控制器
	go func() {
		var key rune
		for {
			key = <-keyPresses
			if key == 'q' {
				err = server.Call("Server.CountAliveCells", countReq, &countRes)
				turn = countRes.CurrentTurn
				quit <- true
			} else if key == 'k' {
				err = server.Call("Server.GetWorld", worldReq, &worldRes)
				dialError(err, c)
				outputPGM(c, p, worldRes.GolBoard.CurrentTurn, worldRes.GolBoard.World)
				turn = worldRes.GolBoard.CurrentTurn
				stopRes := stubs.StopResponse{}
				err = server.Call("Server.Stop", stubs.StopRequest{}, &stopRes)
				quit <- true
			} else if key == 'p' {
				pauseRes := stubs.PauseResponse{}
				err = server.Call("Server.Pause", stubs.PauseRequest{}, &pauseRes)
				dialError(err, c)
				c.events <- StateChange{CompletedTurns: pauseRes.CurrentTurn, NewState: Paused}
				ticker.Stop()
				paused := true
				for paused {
					key = <-keyPresses
					if key == 'p' {
						res := stubs.PauseResponse{}
						err = server.Call("Server.Pause", stubs.PauseRequest{}, &res)
						dialError(err, c)
						ticker.Reset(2 * time.Second) // 重新开始ticker计时
						paused = false
						c.events <- StateChange{CompletedTurns: res.CurrentTurn, NewState: Executing}
					}
				}
			} else if key == 's' {
				err = server.Call("Server.GetWorld", worldReq, &worldRes)
				dialError(err, c)
				go outputPGM(c, p, worldRes.GolBoard.CurrentTurn, worldRes.GolBoard.World)
			}
		}
	}()

	finishFlag := false
	for {
		select {
		case <-finalTurnFinish:
			finishFlag = true
			err = server.Call("Server.GetWorld", worldReq, &worldRes)
			turn = worldRes.GolBoard.CurrentTurn
			dialError(err, c)
			outputPGM(c, p, worldRes.GolBoard.CurrentTurn, worldRes.GolBoard.World)
			c.events <- FinalTurnComplete{res.GolBoard.CurrentTurn, findAliveCells(p, res.GolBoard.World)}
		case <-quit:
			finishFlag = true
		case <-countFinish:
			c.events <- AliveCellsCount{CompletedTurns: countRes.CurrentTurn, CellsCount: countRes.Count}
		}
		if finishFlag {
			break
		}
	}

	// TODO: Report the final state using FinalTurnCompleteEvent.

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
