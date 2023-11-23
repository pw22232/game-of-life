package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"sync"
	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

var Debug bool
var Nodes int
var NodesList = [...]stubs.ServerAddress{
	{Address: "localhost", Port: "8081"},
	{Address: "localhost", Port: "8082"},
	{Address: "localhost", Port: "8083"},
	{Address: "localhost", Port: "8084"},
	{Address: "localhost", Port: "8085"},
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
	serverList  []*rpc.Client
}

func handleError(err error) {
	fmt.Println("Error:", err)
	os.Exit(1)
}

func copyWorld(height, width int, world [][]uint8) [][]uint8 {
	newWorld := make([][]uint8, height)
	for i := range newWorld {
		newWorld[i] = make([]uint8, width)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			newWorld[y][x] = world[y][x]
		}
	}
	return newWorld
}

// RunGol distributor divides the work between workers and interacts with other goroutines.
func (b *Broker) RunGol(req stubs.RunGolRequest, res *stubs.RunGolResponse) (err error) {
	//结束Broker当前的Gol并开始新的Gol
	if b.paused {
		b.processLock.Unlock()
	}
	if b.working {
		b.quit <- true
	}
	b.processLock.Lock()
	b.quit = make(chan bool)
	b.working = true
	turn := 0
	// 初始化Broker
	b.currentTurn = 0
	b.worldWidth = req.GolBoard.Width
	b.worldHeight = req.GolBoard.Height
	b.world = req.GolBoard.World
	threads := req.Threads
	height := b.worldHeight
	width := b.worldWidth
	if len(b.serverList) == 0 {
		b.serverList = make([]*rpc.Client, 0, Nodes)
		connectedNode := 0
		for i := range NodesList {
			server, nodeErr := rpc.Dial("tcp", NodesList[i].Address+":"+NodesList[i].Port)
			if nodeErr == nil {
				connectedNode += 1
				b.serverList = append(b.serverList, server)
			}
			if connectedNode == Nodes {
				break
			}
		}
		Nodes = connectedNode
	}
	b.processLock.Unlock()

	averageHeight := req.GolBoard.Height / Nodes
	restHeight := req.GolBoard.Height % Nodes
	size := averageHeight

	//if Debug {
	//	req.Turns = 100
	//}
	// 根据需要处理的回合数量进行循环
	for turn = 0; turn < req.Turns; turn++ {
		b.processLock.Lock()
		world := copyWorld(b.worldWidth, b.worldHeight, b.world)
		if Debug {
			fmt.Println(turn)
			printWorld(world)
		}
		serverList := b.serverList
		b.processLock.Unlock()
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
			go awsWorker(serverList[i], threads, currentHeight, currentHeight+size, width, height, world, outChannel)
			currentHeight += size
		}
		var flippedCells []util.Cell
		for i := 0; i < Nodes; i++ {
			flippedCells = append(flippedCells, <-outChannels[i]...)
		}
		if Debug {
			fmt.Println(flippedCells)
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
			err = errors.New("broker closed")
			return
		default:
			break
		}
	}
	if Debug {
		printWorld(b.world)
	}
	res.GolBoard = stubs.GolBoard{World: b.world, CurrentTurn: b.currentTurn}
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
	b.quit <- true
	b.processLock.Lock()
	fmt.Println("Gol stopped")
	for _, server := range b.serverList {
		err = server.Call("Server.Stop", stubs.StopRequest{}, stubs.StopResponse{})
	}
	fmt.Println("Broker stopped")
	os.Exit(1)
	return
}

func awsWorker(server *rpc.Client, threads, startY, endY, width, height int, world [][]uint8, out chan<- []util.Cell) {
	req := stubs.NextTurnRequest{
		UpperHalo:  world[(startY-1+height)%height],
		DownerHalo: world[(endY+height)%height],
		GolBoard:   stubs.GolBoard{World: world[startY:endY], Height: endY - startY, Width: width},
		Threads:    threads,
	}
	if Debug {
		fmt.Println(server, "\n", world[(startY-1+height)%height], "\n", world[startY:endY], "\n", world[(endY+height)%height])
	}
	res := stubs.NextTurnResponse{}
	err := server.Call("Server.NextTurn", req, &res)
	if err != nil {
		handleError(err)
	}
	for i := range res.FlippedCells {
		res.FlippedCells[i].Y += startY - 1
	}
	out <- res.FlippedCells
}

func main() {
	portPtr := flag.String("port", "8080", "port to listen on")
	nodePtr := flag.Int("node", 4, "number of node to connect")
	debugPtr := flag.Bool("debug", false, "is debug mode")
	flag.Parse()
	Debug = *debugPtr
	Nodes = *nodePtr
	if Debug {
		fmt.Println("Running in debug mode")
	}
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

func printWorld(world [][]uint8) {
	for n, i := range world {
		fmt.Println(n, i)
	}
}
