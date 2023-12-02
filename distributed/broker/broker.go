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

var Nodes int
var NodesList = [...]stubs.ServerAddress{
	{Address: "localhost", Port: "8081"},
	{Address: "localhost", Port: "8082"},
	{Address: "localhost", Port: "8083"},
	{Address: "localhost", Port: "8084"},
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

func (b *Broker) RunGol(req stubs.RunGolRequest, _ *stubs.RunGolResponse) (err error) {
	if b.paused {
		b.processLock.Unlock()
	}
	if b.working {
		b.quit <- true
	}
	b.quit = make(chan bool)
	b.working = true
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

	averageHeight := req.GolBoard.Height / b.nodes
	restHeight := req.GolBoard.Height % b.nodes
	size := averageHeight
	currentHeight := 0
	for i, server := range b.serverList {
		size = averageHeight
		if i < restHeight {
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
	return
}

func (b *Broker) NextTurn(_ stubs.NextTurnRequest, res *stubs.NextTurnResponse) (err error) {
	currentHeight := 0
	b.processLock.Lock()
	averageHeight := b.worldHeight / b.nodes
	restHeight := b.worldHeight % b.nodes
	world := copyWorld(b.worldWidth, b.worldHeight, b.world)
	b.processLock.Unlock()
	size := averageHeight
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
	b.currentTurn = b.currentTurn + 1
	b.processLock.Unlock()

	select {
	case <-b.quit:
		break
	default:
		break
	}
	b.working = false
	res.FlippedCells = flippedCells
	return
}

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
		res.CurrentTurn = b.currentTurn
		b.processLock.Unlock()
		b.paused = false
	} else {
		b.processLock.Lock()
		b.paused = true
		res.CurrentTurn = b.currentTurn
	}
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
