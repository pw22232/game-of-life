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

// Nodes 是一个全局变量，代表broker最多连接到几台服务器
// （其实这里应该写成在broker对象创建时的一个属性，但是当时没想到这个就直接用全局变量了）
var Nodes int

// NodesList 是一个全局变量，保存了broker能连接的服务器的地址
var NodesList = [...]stubs.ServerAddress{
	{Address: "localhost", Port: "8081"},
	{Address: "localhost", Port: "8082"},
	{Address: "localhost", Port: "8083"},
	{Address: "localhost", Port: "8084"},
}

// Server 是一个类型，代表成功连接的一台服务器
type Server struct {
	ServerRpc     *rpc.Client         // 保存连接到的服务器的rpc pointer，之后每次调用服务器方法时就不用重新连接了
	ServerAddress stubs.ServerAddress // 保存连接到的服务器的地址，用于初始化服务器时传递光环交换服务器的地址
}

// Broker 类型保存Broker所有的属性，Broker作为一个对象（面对对象）
type Broker struct {
	world       [][]uint8 // 2D数组，世界
	worldWidth  int       // 高度，宽度，线程
	worldHeight int
	currentTurn int
	working     bool       // broker是否正在运行
	paused      bool       // broker是否被暂停
	processLock sync.Mutex // 互斥锁，用于在读写world数据时防止race
	quit        chan bool  // 用户按k时通知broker关闭的通道
	serverList  []Server   // 存储了所有已连接的server的数组
	nodes       int        // nodes是已连接的服务器的数量（等同于len(serverList)，其实不是很必要）
}

// handleError 在发生错误时输出错误并退出程序
func handleError(err error) {
	fmt.Println("Error:", err)
	os.Exit(1)
}

// copyWorld 复制一个世界的所有值到一个新的2D数组（防race）
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

// RunGol 初始化broker（已初始化的broker则会重置），并且初始化所有连接到的服务器
func (b *Broker) RunGol(req stubs.RunGolRequest, _ *stubs.RunGolResponse) (err error) {
	if b.paused { // 如果broker暂停时被重置，则先解锁互斥锁（其实这里调用自己的暂停方法会更好）
		b.processLock.Unlock()
	}
	if b.working { // 如果broker正在工作
		b.quit <- true // 向通道传递退出信号
	}
	b.quit = make(chan bool)
	b.working = true // 注意：这行代码的位置不对！应该放在底下NextTurn刚开始！现在的位置在极少数情况可能导致race
	b.currentTurn = 0
	b.worldWidth = req.GolBoard.Width
	b.worldHeight = req.GolBoard.Height
	b.world = req.GolBoard.World
	if len(b.serverList) == 0 { // 如果broker没有连接过服务器则尝试连接，否则直接用原来连接到的
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
