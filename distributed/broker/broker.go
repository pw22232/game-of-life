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

var Nodes int
var NodesList = [...]stubs.ServerAddress{
	{Address: "54.163.25.144", PrivateAddress: "172.31.19.154", Port: "8080"},
	{Address: "54.242.220.90", PrivateAddress: "172.31.23.15", Port: "8080"},
}

type Server struct {
	ServerRpc     *rpc.Client
	ServerAddress stubs.ServerAddress
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
	serverList  []Server
	nodes       int
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
	b.quit = make(chan bool)
	b.working = true
	turn := 0
	// 初始化Broker
	b.currentTurn = 0
	b.worldWidth = req.GolBoard.Width
	b.worldHeight = req.GolBoard.Height
	b.world = req.GolBoard.World
	if len(b.serverList) == 0 {
		b.serverList = make([]Server, 0, Nodes)
		connectedNode := 0
		for i := range NodesList {
			server, nodeErr := rpc.Dial("tcp", NodesList[i].Address+":"+NodesList[i].Port)
			if nodeErr == nil {
				connectedNode += 1
				b.serverList = append(b.serverList, Server{ServerRpc: server, ServerAddress: NodesList[i]})
			}
			if connectedNode == Nodes {
				break
			}
		}
		b.nodes = connectedNode
	}

	// 初始化分布式节点
	averageHeight := req.GolBoard.Height / b.nodes
	restHeight := req.GolBoard.Height % b.nodes
	size := averageHeight
	currentHeight := 0
	for i, server := range b.serverList {
		size = averageHeight
		if i < restHeight {
			// 将除不尽的部分分配到前几个threads中，每个threads一行
			size += 1
		}
		err = server.ServerRpc.Call("Server.Init", stubs.InitRequest{
			GolBoard: stubs.GolBoard{
				World:  req.GolBoard.World[currentHeight : currentHeight+size],
				Height: size,
				Width:  req.GolBoard.Width},
			Threads:        req.Threads,
			PreviousServer: b.serverList[(i-1+b.nodes)%b.nodes].ServerAddress,
			NextServer:     b.serverList[(i+1+b.nodes)%b.nodes].ServerAddress,
		}, &stubs.InitResponse{})
		currentHeight += size
		if err != nil {
			handleError(err)
		}
	}
	// 根据需要处理的回合数量进行循环
	for turn = 0; turn < req.Turns; turn++ {
		currentHeight = 0
		b.processLock.Lock()
		world := copyWorld(b.worldWidth, b.worldHeight, b.world)
		b.processLock.Unlock()
		var outChannels []chan []util.Cell
		for i := 0; i < b.nodes; i++ {
			size = averageHeight
			if i < restHeight {
				size += 1
			}
			outChannel := make(chan []util.Cell)
			outChannels = append(outChannels, outChannel)
			go callNextTurn(b.serverList[i].ServerRpc, currentHeight, outChannel)
			currentHeight += size
		}
		var flippedCells []util.Cell
		for i := 0; i < b.nodes; i++ {
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
			err = errors.New("broker closed")
			return
		default:
			break
		}
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
		err = server.ServerRpc.Call("Server.Stop", stubs.StopRequest{}, stubs.StopResponse{})
	}
	fmt.Println("Broker stopped")
	os.Exit(1)
	return
}

func callNextTurn(server *rpc.Client, startY int, out chan<- []util.Cell) {
	res := stubs.NextTurnResponse{}
	err := server.Call("Server.NextTurn", stubs.NextTurnRequest{}, &res)
	if err != nil {
		handleError(err)
	}
	for i := range res.FlippedCells {
		res.FlippedCells[i].Y += startY
	}
	out <- res.FlippedCells
}

func main() {
	portPtr := flag.String("port", "8080", "port to listen on")
	nodePtr := flag.Int("node", 4, "number of node to connect")
	flag.Parse()
	Nodes = *nodePtr
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
