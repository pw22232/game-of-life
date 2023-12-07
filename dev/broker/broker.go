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
	if b.working {
		b.quit <- true
	}
	b.processLock.Lock()
	b.quit = make(chan bool)
	b.working = true
	// 初始化Broker
	b.currentTurn = 0
	b.worldWidth = req.GolBoard.Width
	b.worldHeight = req.GolBoard.Height
	b.world = req.GolBoard.World
	connectedNode := 0
	if len(b.serverList) == 0 {
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
		if connectedNode < 1 {
			err = errors.New("no node connected")
			return
		}
	} else {
		connectedNode = len(b.serverList)
	}

	// 初始化分布式节点
	averageHeight := req.GolBoard.Height / connectedNode
	restHeight := req.GolBoard.Height % connectedNode
	size := averageHeight
	currentHeight := 0
	for i := 0; i < connectedNode; i++ {
		size = averageHeight
		if i < restHeight {
			// 将除不尽的部分分配到前几个Server里，每个Server一行
			size += 1
		}
		initErr := b.serverList[i].ServerRpc.Call("Server.InitServer", stubs.InitServerRequest{
			GolBoard: stubs.GolBoard{
				CurrentTurn: req.GolBoard.CurrentTurn,
				World:       req.GolBoard.World[currentHeight : currentHeight+size],
				Height:      size,
				Width:       req.GolBoard.Width},
			Threads:        req.Threads,
			Turns:          req.Turns,
			StartY:         currentHeight,
			PreviousServer: b.serverList[(i-1+connectedNode)%connectedNode].ServerAddress,
			NextServer:     b.serverList[(i+1+connectedNode)%connectedNode].ServerAddress,
		}, &stubs.InitServerResponse{})
		if initErr != nil {
			handleError(initErr)
		}
		currentHeight += size
	}

	// 启动所有服务器
	var outChannels []chan [][]uint8
	for i := 0; i < connectedNode; i++ {
		server := b.serverList[i].ServerRpc
		outChannel := make(chan [][]uint8)
		outChannels = append(outChannels, outChannel)
		go func(outChannel chan [][]uint8) {
			runReq := stubs.RunServerResponse{}
			runErr := server.Call("Server.RunServer", stubs.RunServerRequest{}, &runReq)
			if runErr != nil {
				handleError(runErr)
			}
			outChannel <- runReq.World
		}(outChannel)
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

	b.processLock.Lock()
	b.working = false
	b.processLock.Unlock()
	res.GolBoard = stubs.GolBoard{World: finalWorld, CurrentTurn: req.Turns}
	return
}

func (b *Broker) CountAliveCells(_ stubs.AliveCellsCountRequest, res *stubs.AliveCellsCountResponse) (err error) {
	aliveCellsCount := 0
	worldRes := stubs.CurrentWorldResponse{}
	worldErr := b.GetWorld(stubs.CurrentWorldRequest{}, &worldRes)
	if worldErr != nil {
		handleError(worldErr)
	}
	b.processLock.Lock()
	width := b.worldWidth
	height := b.worldHeight
	b.processLock.Unlock()

	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			if worldRes.World[y][x] == 255 {
				aliveCellsCount++
			}
		}
	}
	res.Count = aliveCellsCount
	res.CurrentTurn = worldRes.CurrentTurn
	return
}

func (b *Broker) GetWorld(_ stubs.CurrentWorldRequest, res *stubs.CurrentWorldResponse) (err error) {
	var outChannels []chan stubs.WorldChangeResponse
	for i := range b.serverList {
		outChannel := make(chan stubs.WorldChangeResponse)
		outChannels = append(outChannels, outChannel)
		server := b.serverList[i]
		go func() {
			worldRes := stubs.WorldChangeResponse{}
			worldErr := server.ServerRpc.Call("Server.GetWorldChange",
				&stubs.WorldChangeRequest{}, &worldRes)
			if worldErr != nil {
				handleError(worldErr)
			}
			outChannel <- worldRes
		}()
	}
	var responses []stubs.WorldChangeResponse
	for i := range b.serverList {
		responses = append(responses, <-outChannels[i])
	}
	turn := responses[0].CurrentTurn
	for i := range responses {
		if responses[i].CurrentTurn < turn {
			turn = responses[i].CurrentTurn
		}
	}
	var flippedCells []util.Cell
	for i := range responses {
		if responses[i].CurrentTurn-turn == 0 {
			for _, flippedCell := range responses[i].FlippedCellsBuffer {
				if responses[i].FlippedCellsMap[flippedCell] {
					delete(responses[i].FlippedCellsMap, flippedCell)
				} else {
					responses[i].FlippedCellsMap[flippedCell] = true
				}
			}
		} else if responses[i].CurrentTurn-turn > 1 {
			err = errors.New("server not sync")
		}
		for flippedCell := range responses[i].FlippedCellsMap {
			flippedCells = append(flippedCells, flippedCell)
		}
	}
	b.processLock.Lock()
	world := copyWorld(b.worldHeight, b.worldWidth, b.world)
	b.processLock.Unlock()
	for _, flippedCell := range flippedCells {
		if world[flippedCell.Y][flippedCell.X] == 255 {
			world[flippedCell.Y][flippedCell.X] = 0
		} else {
			world[flippedCell.Y][flippedCell.X] = 255
		}
	}

	res.CurrentTurn = turn
	res.World = world
	return
}

func (b *Broker) Pause(_ stubs.PauseRequest, res *stubs.PauseResponse) (err error) {
	pauseRes := stubs.PauseResponse{}
	pauseErr := b.serverList[0].ServerRpc.Call("Server.Pause", stubs.PauseRequest{}, &pauseRes)
	if pauseErr != nil {
		handleError(pauseErr)
	}
	res.CurrentTurn = pauseRes.CurrentTurn
	return
}

func (b *Broker) Stop(_ stubs.StopRequest, _ *stubs.StopResponse) (err error) {
	b.quit <- true
	for _, server := range b.serverList {
		_ = server.ServerRpc.Call("Server.Stop", stubs.StopRequest{}, stubs.StopResponse{})
	}
	fmt.Println("Broker stopped")
	os.Exit(1)
	return
}

// broker的主函数
func main() {
	// 设置broker监听指定的端口，flag可以接收用户运行时输入的参数，例如go run . -port 8080
	portPtr := flag.String("port", "8080", "port to listen on")
	// 设置broker最多连接到几台服务器，默认为4台服务器
	nodePtr := flag.Int("node", 4, "number of node to connect")
	flag.Parse()
	// 设置全局变量Nodes
	Nodes = *nodePtr

	// 开始监听，address的:前面不加任何东西代表监听本机所有的网络
	ln, err := net.Listen("tcp", ":"+*portPtr)
	if err != nil {
		handleError(err)
		return
	}
	// 在主程序退出之后停止监听（不过这行代码没有什么用）
	defer func() {
		_ = ln.Close()
	}()
	// 创建一个Broker对象，并将它的所有方法都发布（发布订阅模型）
	// 这样连接到这个broker的客户端可以直接调用broker对象所有的方法
	_ = rpc.Register(new(Broker))
	fmt.Println("Broker Start, Listening on " + ln.Addr().String())
	// Accept后所有其他程序就都能连接到这个程序了，Accept会阻塞整个程序直到对应的监听ln.close掉
	rpc.Accept(ln)
}
