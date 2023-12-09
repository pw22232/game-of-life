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
	paused      bool       // 这个变量没有意义（因为在自动运行的光环中，broker不需要暂停）
	processLock sync.Mutex // 互斥锁，用于在读写world数据时防止race
	quit        chan bool  // 用户按k时通知broker关闭的通道
	serverList  []Server   // 存储了所有已连接的server的数组
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

// RunGol 初始化broker（已初始化的broker则会重置），并且初始化所有连接到的服务器，然后让所有服务器开始运行
func (b *Broker) RunGol(req stubs.RunGolRequest, res *stubs.RunGolResponse) (err error) {
	if b.working { // 如果broker正在工作
		b.quit <- true // 向通道传递退出信号
	}
	// 这里锁定互斥锁其实没有用，因为这些broker初始化完成前不会有其他函数读取数据
	b.processLock.Lock()
	b.quit = make(chan bool)
	b.working = true // 设置broker为正在工作
	// 初始化Broker
	b.currentTurn = 0
	b.worldWidth = req.GolBoard.Width
	b.worldHeight = req.GolBoard.Height
	b.world = req.GolBoard.World
	connectedNode := 0
	if len(b.serverList) == 0 { // 如果broker没有连接过服务器则尝试连接，否则直接用原来连接到的
		// 这里应该用make创建数组，但是忘记了，不过不创建也没事，因为go在初始化broker时会自动创建数组
		for i := range NodesList { // 从全局变量里获取服务器地址
			server, nodeErr := rpc.Dial("tcp", NodesList[i].Address+":"+NodesList[i].Port) // 尝试连接服务器
			// 如果没有发生错误则代表连接成功，连接数量+1，将服务器的pointer和地址放进serverList
			if nodeErr == nil {
				connectedNode += 1
				b.serverList = append(b.serverList, Server{ServerRpc: server, ServerAddress: NodesList[i]})
			}
			if connectedNode == Nodes { // 如果连接的服务器数量已经达到上限则停止循环
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

	// 分割棋盘并初始化服务器，分割逻辑和parallel一致
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
		// 这里调用服务器的初始化方法，传递一个分割过的棋盘（含服务器要处理的那部分世界的数据和高度宽度）
		// 同时传递总计算的回合数，使用线程的数量和前后两台用于光环交换的服务器的地址
		// 以及服务器Y轴的开始位置（这样服务器返回细胞坐标给broker时直接y轴加上指定的值，后面broker就不用再算了）
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
		// 直接在新的线程里调用服务器的RunServer方法，这个方法直到最后一回合计算完成才会返回计算过后的世界（2D数组）
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
	// 收集服务器运行完成后的结果
	finalWorld := make([][]uint8, 0, req.GolBoard.Height)
	for i := 0; i < connectedNode; i++ {
		select {
		// 等待服务器运行完成将结果传入通道，然后接收
		case output := <-outChannels[i]:
			finalWorld = append(finalWorld, output...)
		case <-b.quit: // 收集过程中用户按k退出，直接抛出错误并返回
			err = errors.New("broker closed")
			return
		}
	}
	// 运行完成后改变broker的工作状态
	b.processLock.Lock()
	b.working = false
	b.processLock.Unlock()
	// 返回最终的世界和运行的回合
	res.GolBoard = stubs.GolBoard{World: finalWorld, CurrentTurn: req.Turns}
	return
}

// CountAliveCells 调用 GetWorld 获取服务器当前数据，然后返回存活细胞的数量和当前回合数
func (b *Broker) CountAliveCells(_ stubs.AliveCellsCountRequest, res *stubs.AliveCellsCountResponse) (err error) {
	aliveCellsCount := 0
	// 因为服务器完全自动运行，这里要先调用自己的GetWorld方法，获取服务器当前的世界和回合
	worldRes := stubs.CurrentWorldResponse{}
	worldErr := b.GetWorld(stubs.CurrentWorldRequest{}, &worldRes)
	if worldErr != nil {
		handleError(worldErr)
	}
	// 获取属性时锁定互斥锁
	b.processLock.Lock()
	width := b.worldWidth
	height := b.worldHeight
	b.processLock.Unlock()
	// 计算逻辑和parallel的一致
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			if worldRes.World[y][x] == 255 {
				aliveCellsCount++
			}
		}
	}
	// 返回当前的回合和存活细胞数量
	res.Count = aliveCellsCount
	res.CurrentTurn = worldRes.CurrentTurn
	return
}

// GetWorld 是自动光环重要！！！的函数，会向所有服务器获取数据，然后整合成一个特定回合的世界
func (b *Broker) GetWorld(_ stubs.CurrentWorldRequest, res *stubs.CurrentWorldResponse) (err error) {
	// 为了保证服务器的状态同步以及效率够高，收集服务器信息的调用都放在单独的go线程中。因此需要通道来收集结果
	var outChannels []chan stubs.WorldChangeResponse
	for i := range b.serverList {
		outChannel := make(chan stubs.WorldChangeResponse) // 为调用每个服务器的go线程创建通道
		outChannels = append(outChannels, outChannel)
		server := b.serverList[i]
		// 调用服务器的GetWorldChange方法，这个方法会返回上一回合的世界和最开始的世界之间对比，哪些细胞发生了变化 FlippedCellsMap
		// 以及服务器运行完成的回合数，和这个回合哪些细胞发生了变化 FlippedCellsBuffer
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
	// 收集所有结果
	var responses []stubs.WorldChangeResponse
	for i := range b.serverList {
		responses = append(responses, <-outChannels[i])
	}
	// 因为服务器完全自动运行，所以不可避免的会出现有的服务器比其他服务器快一个回合
	// 这里比较每个服务器返回的当前回合，取回合数最小（最慢的服务器）的作为这次调用的回合。
	turn := responses[0].CurrentTurn
	for i := range responses {
		if responses[i].CurrentTurn < turn {
			turn = responses[i].CurrentTurn
		}
	}
	// 检查所有服务器返回的结果
	// 如果服务器和上面获取的回合数一样（代表慢），那就把它返回的世界变化加上它返回的当前回合的变化，变成当前回合的世界变化
	// 如果服务器快一回合，那它返回的世界变化就是我们要的（上一回合的世界变化）
	// 如果快不止一回合就说明服务器同步出错了，那就直接抛出错误
	// 举个例子，假设4台服务器现在分别在处理，880、881、880、880回合。这时broker要获取数据：
	// 则3台服务器返回直到879回合的时候有哪些细胞发生了变化的字典，以及880这回合发生的变化的buffer数组
	// 那1台快的服务器返回直到880回合的时候有哪些细胞发生了变化的字典，以及881这回合发生的变化的buffer数组
	// 我们接受到数据以后发现880是最慢的回合，则把三台880回合服务器返回的879字典加上buffer数组，这样就变成了880的世界变化
	// 那台881的服务器的字典已经代表了880回合的世界变化，所以不用改
	// 这样我们就能算出最开始的世界到880回合的世界中哪些细胞发生了变化
	var flippedCells []util.Cell
	for i := range responses {
		if responses[i].CurrentTurn-turn == 0 {
			// 这里把buffer列表里变化的细胞应用到字典里，字典的key是细胞的坐标
			// 如果字典里没有指定坐标的key，那就代表这个细胞原来没变，buffer里这回合这个细胞变了，那就要加上这个key
			// 如果字典已经有这个细胞坐标的key就代表那个细胞坐标原来就要变化，变两次等于不变，因此删除这个key
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
		// 最后将字典所有的key导出，就能算出有哪些细胞的存活状态对比最开始的世界发生变化了
		for flippedCell := range responses[i].FlippedCellsMap {
			flippedCells = append(flippedCells, flippedCell)
		}
	}
	// 复制世界时锁定互斥锁，之后改复制的世界时就不用锁着互斥锁了，提高效率
	// 其实不锁也没事，因为这个版本中broker的世界是不会变的，一直保持初始状态下的世界
	b.processLock.Lock()
	world := copyWorld(b.worldHeight, b.worldWidth, b.world)
	b.processLock.Unlock()
	// 把上面算出来的变化应用到最开始的世界上，就能算出这回合的世界长什么样了
	for _, flippedCell := range flippedCells {
		if world[flippedCell.Y][flippedCell.X] == 255 {
			world[flippedCell.Y][flippedCell.X] = 0
		} else {
			world[flippedCell.Y][flippedCell.X] = 255
		}
	}
	// 返回我们算出来的回合数和世界
	res.CurrentTurn = turn
	res.World = world
	return
}

// Pause 只调用服务器的暂停方法，因为broker不负责任何计算所以不需要暂停
func (b *Broker) Pause(_ stubs.PauseRequest, res *stubs.PauseResponse) (err error) {
	pauseRes := stubs.PauseResponse{}
	pauseErr := b.serverList[0].ServerRpc.Call("Server.Pause", stubs.PauseRequest{}, &pauseRes)
	if pauseErr != nil {
		handleError(pauseErr)
	}
	res.CurrentTurn = pauseRes.CurrentTurn
	return
}

// Stop 通知知所有服务器关闭，然后关闭broker
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
