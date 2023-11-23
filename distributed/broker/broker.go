package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"sync"
	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

var Nodes = 4
var NodesList = [...]stubs.ServerAddress{
	{Address: "localhost", Port: "8081"},
	{Address: "localhost", Port: "8082"},
	{Address: "localhost", Port: "8083"},
	{Address: "localhost", Port: "8084"},
}

type Broker struct {
	world       [][]uint8
	worldWidth  int
	worldHeight int
	currentTurn int
	working     bool
	paused      bool
	processLock sync.Mutex
	quit        chan bool
}

func handleError(err error) {
	fmt.Println("Error:", err)
}

// RunGol distributor divides the work between workers and interacts with other goroutines.
func (b *Broker) RunGol(req stubs.RunGolRequest, res *stubs.RunGolResponse) (err error) {
	//结束服务器当前的Gol并开始新的Gol
	if b.paused {
		b.processLock.Unlock()
	}
	if b.working {
		b.quit <- true
	}
	b.working = true
	// 根据需要处理的回合数量进行循环
	world := req.GolBoard.World
	turn := 0
	b.processLock.Lock()
	b.currentTurn = 0
	b.world = req.GolBoard.World
	b.worldHeight = req.GolBoard.Height
	b.worldWidth = req.GolBoard.Width
	b.processLock.Unlock()
	averageHeight := req.GolBoard.Height / req.Threads
	restHeight := req.GolBoard.Height % req.Threads
	size := averageHeight

	for turn = 0; turn < req.Turns; turn++ {
		var outChannels []chan []util.Cell
		currentHeight := 0
		for i := 0; i < Nodes; i++ {
			size = averageHeight
			if i < restHeight {
				// 将除不尽的部分分配到前几个threads中，每个threads一行
				size += 1
			}
			outChannel := make(chan []util.Cell)
			outChannels = append(outChannels, outChannel)
			go worker(currentHeight, currentHeight+size, req.GolBoard.Width, req.GolBoard.Height, world, outChannel)
			currentHeight += size
		}
		var flippedCells []util.Cell
		for i := 0; i < req.Threads; i++ {
			flippedCells = append(flippedCells, <-outChannels[i]...)
		}
		for _, flippedCell := range flippedCells {
			if world[flippedCell.Y][flippedCell.X] == 255 {
				world[flippedCell.Y][flippedCell.X] = 0
			} else {
				world[flippedCell.Y][flippedCell.X] = 255
			}
		}
		b.processLock.Lock()
		b.world = world
		b.currentTurn = turn + 1
		b.processLock.Unlock()

		select {
		case <-b.quit:
			return
		default:
			break
		}
	}
	res.GolBoard = stubs.GolBoard{World: world, CurrentTurn: turn}
	b.working = false
	return
}

// CountAliveCells 补充注释
func (b *Broker) CountAliveCells(_ stubs.AliveCellsCountRequest, res *stubs.AliveCellsCountResponse) (err error) {
	aliveCellsCount := 0
	b.processLock.Lock()
	for x := 0; x < b.worldWidth; x++ {
		for y := 0; y < b.worldHeight; y++ {
			if b.world[y][x] == 255 {
				aliveCellsCount++
			}
		}
	}
	res.Count = aliveCellsCount
	res.CurrentTurn = b.currentTurn
	b.processLock.Unlock()
	return
}

func (b *Broker) GetWorld(_ stubs.CurrentWorldRequest, res *stubs.CurrentWorldResponse) (err error) {
	b.processLock.Lock()
	res.GolBoard = stubs.GolBoard{World: b.world, CurrentTurn: b.currentTurn, Width: b.worldWidth, Height: b.worldHeight}
	b.processLock.Unlock()
	return
}

func (b *Broker) Pause(_ stubs.PauseRequest, res *stubs.PauseResponse) (err error) {
	if b.paused {
		b.processLock.Unlock()
		b.paused = false
	} else {
		b.processLock.Lock()
		b.paused = true
	}
	res.CurrentTurn = b.currentTurn
	return
}

func (b *Broker) Stop(_ stubs.StopRequest, _ *stubs.StopResponse) (err error) {
	fmt.Println("Broker stopped")
	os.Exit(0)
	return
}

func worker(startY, endY, width, height int, world [][]uint8, out chan<- []util.Cell) {
	out <- calculateNextState(startY, endY, width, height, world)
}

func main() {
	portPtr := flag.String("port", "8080", "port to listen on")
	flag.Parse()

	ln, err := net.Listen("tcp", ":"+*portPtr)
	if err != nil {
		handleError(err)
		return
	}
	defer func() {
		_ = ln.Close()
	}()
	_ = rpc.Register(new(Broker))
	fmt.Println("Broker Start, Listening on " + ln.Addr().String())
	rpc.Accept(ln)
}
