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
)

var Nodes int
var NodesList = [...]stubs.ServerAddress{
	{Address: "54.163.25.144", Port: "8080"},
	{Address: "54.242.220.90", Port: "8080"},
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
}

func handleError(err error) {
	fmt.Println("Error:", err)
	os.Exit(1)
}

// RunGol distributor divides the work between workers and interacts with other goroutines.
func (b *Broker) RunGol(req stubs.RunGolRequest, res *stubs.RunGolResponse) (err error) {
	//结束Broker当前的Gol并开始新的Gol
	b.processLock.Lock()
	if b.working {
		b.quit <- true
	}
	b.quit = make(chan bool)
	b.working = true
	// 初始化Broker
	b.currentTurn = 0
	b.worldWidth = req.GolBoard.Width
	b.worldHeight = req.GolBoard.Height
	b.world = req.GolBoard.World
	connectedNode := 0
	if len(b.serverList) == 0 {
		b.serverList = make([]Server, 0, Nodes)
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
	}
	// 初始化分布式节点
	averageHeight := req.GolBoard.Height / connectedNode
	restHeight := req.GolBoard.Height % connectedNode
	size := averageHeight
	currentHeight := 0
	var outChannels []chan [][]uint8

	for i := 0; i < connectedNode; i++ {
		size = averageHeight
		if i < restHeight {
			// 将除不尽的部分分配到前几个Server里，每个Server一行
			size += 1
		}
		outChannel := make(chan [][]uint8)
		outChannels = append(outChannels, outChannel)
		server := b.serverList[i]
		previousServer := b.serverList[(i-1+connectedNode)%connectedNode]
		nextServer := b.serverList[(i+1+connectedNode)%connectedNode]
		go func() {
			finalRes := stubs.RunServerResponse{}
			err = server.ServerRpc.Call("Server.Init", stubs.RunServerRequest{
				GolBoard: stubs.GolBoard{
					World:  req.GolBoard.World[currentHeight : currentHeight+size],
					Height: size,
					Width:  req.GolBoard.Width},
				Threads:        req.Threads,
				PreviousServer: previousServer.ServerAddress,
				NextServer:     nextServer.ServerAddress,
			}, &finalRes)
			if err != nil {
				handleError(err)
			}
			outChannel <- finalRes.World
		}()
		currentHeight += size
	}
	b.processLock.Unlock()

	finalWorld := make([][]uint8, 0, req.GolBoard.Height)
	for i := 0; i < connectedNode; i++ {
		select {
		case output := <-outChannels[i]:
			finalWorld = append(finalWorld, output...)
		case <-b.quit:
			err = errors.New("broker closed")
			return
		}
	}

	res.GolBoard = stubs.GolBoard{World: finalWorld, CurrentTurn: req.Turns}
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
	var outChannels []chan stubs.WorldChangeResponse

	for i := range b.serverList {
		outChannel := make(chan stubs.WorldChangeResponse)
		outChannels = append(outChannels, outChannel)
		server := b.serverList[i]
		go func() {
			worldRes := stubs.WorldChangeResponse{}
			err = server.ServerRpc.Call("Server.GetWorldChange",
				stubs.WorldChangeRequest{}, &worldRes)
			if err != nil {
				handleError(err)
			}
			outChannel <- worldRes
		}()
	}

	b.processLock.Unlock()
	return
}

func (b *Broker) Pause(_ stubs.PauseRequest, res *stubs.PauseResponse) (err error) {
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
