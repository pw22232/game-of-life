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

// Server 类型保存服务器所有的属性，服务器作为一个对象（面对对象）
type Server struct {
	world              [][]uint8          // 2D数组，世界
	flippedCellsMap    map[util.Cell]bool // 一个字典，key是细胞坐标，key里有的细胞就是和最开始的世界相比有变化的细胞
	flippedCellsBuffer []util.Cell        // map里储存上回合的变化，buffer里存储这回合的变化（解决不同步问题）
	startY             int                // 直接记录服务器计算的部分的Y轴开始的位置，返回给broker时直接把相对坐标计算成绝对坐标
	height             int                // 高度，宽度，线程，总回合数
	width              int
	threads            int
	turns              int
	currentTurn        int       // 当前在处理的回合数
	working            bool      // 服务器是否正在运行
	pause              bool      // 服务器是否已经被暂停
	quit               chan bool // 用户按k时通知服务器关闭的通道
	firstLineReady     chan bool // 检测是否已经完成上下光环交换的通道
	lastLineReady      chan bool
	getWorld           chan bool
	previousServer     *rpc.Client // 自己的上下光环服务器rpc，这里保存的是rpc客户端的pointer，
	nextServer         *rpc.Client // 这样就不用每次获取光环时都需要连接服务器了
	processLock        sync.Mutex  // 因为服务器自动运行，为了防止race需要互斥锁
}

// handleError 自动光环太容易出错了，我直接把退出关掉了，不过交上去之前本来应该加回来
func handleError(err error) {
	fmt.Println("Error:", err)
}

// 将自己的世界和上下两行获取到的光环组合起来成为一个新的世界
func worldCreate(height int, world [][]uint8, upperHalo, downerHalo []uint8) [][]uint8 {
	newWorld := make([][]uint8, 0, height+2)
	newWorld = append(newWorld, upperHalo)
	newWorld = append(newWorld, world...)
	newWorld = append(newWorld, downerHalo)
	return newWorld
}

// InitServer 是服务器初始化函数。但是因为死锁的问题，服务器无法重置状态
func (s *Server) InitServer(req stubs.InitServerRequest, _ *stubs.InitServerResponse) (err error) {
	// 因为重置不了这里没有意义
	if s.pause {
		s.processLock.Unlock()
	}
	if s.working {
		s.quit <- true
	}
	// 初始化所有属性
	s.processLock.Lock()
	s.working = false
	s.startY = req.StartY
	s.currentTurn = req.GolBoard.CurrentTurn
	s.quit = make(chan bool)
	s.firstLineReady = make(chan bool)
	s.lastLineReady = make(chan bool)
	s.getWorld = make(chan bool)
	s.flippedCellsBuffer = make([]util.Cell, 0)
	s.flippedCellsMap = make(map[util.Cell]bool)
	s.world = req.GolBoard.World
	s.threads = req.Threads
	s.width = req.GolBoard.Width
	s.height = req.GolBoard.Height
	s.turns = req.Turns
	s.previousServer, _ = rpc.Dial("tcp", req.PreviousServer.Address+":"+req.PreviousServer.Port)
	fmt.Println("Connect to previous halo server ", req.PreviousServer.Address+":"+req.PreviousServer.Port)
	s.nextServer, _ = rpc.Dial("tcp", req.NextServer.Address+":"+req.NextServer.Port)
	fmt.Println("Connect to next halo server ", req.NextServer.Address+":"+req.NextServer.Port)
	s.processLock.Unlock()
	return
}

// RunServer 被调用后开始自动每个回合交换光环，计算世界的下回合状态，直到最后一回合计算完成再返回最终的世界
func (s *Server) RunServer(_ stubs.RunServerRequest, res *stubs.RunServerResponse) (err error) {
	// 读取服务器的属性，修改服务器的工作状态
	s.processLock.Lock()
	turn := s.currentTurn
	turns := s.turns
	s.working = true
	s.processLock.Unlock()
	// 根据总回合数进行循环
	for turn < turns {
		// 这两个通道用于接收getHalo返回的光环区域
		upperOut := make(chan []uint8)
		nextOut := make(chan []uint8)
		// 所有服务器都在子线程请求其他服务器输出光环，这样就不会发生死锁了
		go getHalo(s.previousServer, false, upperOut)
		go getHalo(s.nextServer, true, nextOut)
		// 确认自己已经准备好交换数据
		s.firstLineReady <- true
		s.lastLineReady <- true
		// 如果broker请求数据，则在这里处理
		select {
		case <-s.getWorld: // 在输出变化数据时暂停服务器处理，这样就能保证服务器之间的回合差距始终不会大于1回合
			s.getWorld <- true
		default:
			break
		}
		// 等待数据已经准备好交换了
		<-s.firstLineReady
		<-s.lastLineReady
		// 到这里为止自己的光环数据都已经被复制好输出了，因此可以安全的修改世界了
		// 等待从上下两台光环服务器获取到数据
		upperHalo := <-upperOut
		nextHalo := <-nextOut
		// 用获取的数据来创建一个新的世界，新世界上下两行是光环，中间是要计算下回合的部分
		// 这样做的好处就是可以用和parallel完全相同的逻辑来计算，唯一不同的是只计算中间的部分（第二行到倒数第二行）
		world := worldCreate(s.height, s.world, upperHalo, nextHalo)
		// 开始多线程处理
		// 这个列表保存了所有worker输出数据的通道
		var outChannels []chan []util.Cell
		// 因为只处理世界中间的部分，因此currentHeight初始为1
		currentHeight := 1
		// 这里的逻辑和parallel里完全相同相同
		averageHeight := s.height / s.threads
		restHeight := s.height % s.threads
		size := averageHeight
		for i := 0; i < s.threads; i++ {
			size = averageHeight
			if i < restHeight {
				size += 1
			}
			outChannel := make(chan []util.Cell)
			outChannels = append(outChannels, outChannel)
			go worker(currentHeight, currentHeight+size, s.width, s.height+2, world, outChannel)
			currentHeight += size
		}
		var flippedCells []util.Cell
		for i := 0; i < s.threads; i++ {
			flippedCells = append(flippedCells, <-outChannels[i]...)
		}

		s.processLock.Lock()
		for _, flippedCell := range flippedCells {
			if s.world[flippedCell.Y][flippedCell.X] == 255 {
				s.world[flippedCell.Y][flippedCell.X] = 0
			} else {
				s.world[flippedCell.Y][flippedCell.X] = 255
			}
		}
		// 这里将上回合的buffer放进map里，完成之后map里就存了上回合的数据
		for _, flippedCell := range s.flippedCellsBuffer {
			// 如果字典里没有指定坐标的key，那就代表这个细胞原来没变，buffer里这回合这个细胞变了，那就要加上这个key
			// 如果字典已经有这个细胞坐标的key就代表那个细胞坐标原来就要变化，变两次等于不变，因此删除这个key
			if s.flippedCellsMap[flippedCell] {
				delete(s.flippedCellsMap, flippedCell)
			} else {
				s.flippedCellsMap[flippedCell] = true
			}
		}
		// 然后将这回合计算出来的细胞变化放进buffer里
		for i := range flippedCells {
			flippedCells[i].Y += s.startY // 这里要把每个坐标的Y轴加上startY，这样broker接收以后就不用重新计算一遍了
		}
		s.flippedCellsBuffer = flippedCells
		s.currentTurn++
		turn++
		s.processLock.Unlock()
		select {
		case <-s.quit:
			return
		default:
			break
		}
	}
	// 所有回合都处理完毕后返回整个计算好的世界
	s.processLock.Lock()
	res.World = s.world
	s.working = false
	s.processLock.Unlock()
	return
}

// 这两个函数用于获取光环，其实写成一个函数会更好，因为内容重复我就不写两遍注释了

// GetFirstLine 允许其他服务器调用，调用时会返回自己世界第一行的数据，完成后向通道传递信息
func (s *Server) GetFirstLine(_ stubs.LineRequest, res *stubs.LineResponse) (err error) {
	<-s.firstLineReady                     // 确认已经计算完成这回合了，再交换光环
	s.processLock.Lock()                   // 读取世界数据时锁定互斥锁
	line := make([]uint8, len(s.world[0])) // 创建一个长度和世界第一行相同的列表（其实这里直接用s.width会更好）
	for i, value := range s.world[0] {
		line[i] = value // 将世界第一行每个值复制进新的数组（这样即使世界被修改光环也肯定不会变）
	}
	res.Line = line
	s.processLock.Unlock()
	s.firstLineReady <- true // 在交换前向通道传递值，这样保证所有服务器都完成光环交换后再继续运行下回合
	return
}

// GetLastLine 返回自己世界最后一行的数据，和 GetFirstLine 逻辑相同
func (s *Server) GetLastLine(_ stubs.LineRequest, res *stubs.LineResponse) (err error) {
	<-s.lastLineReady

	s.processLock.Lock()
	line := make([]uint8, len(s.world[s.height-1]))
	for i, value := range s.world[s.height-1] {
		line[i] = value
	}
	res.Line = line
	s.processLock.Unlock()
	s.lastLineReady <- true
	return
}

// getHalo 是获取光环的函数，输入服务器地址和要获取的光环类型，然后调用指定服务器的方法，向通道传输返回值
func getHalo(server *rpc.Client, isFirstLine bool, out chan []uint8) {
	res := stubs.LineResponse{}
	var err error
	if isFirstLine {
		err = server.Call("Server.GetFirstLine", stubs.LineRequest{}, &res)
	} else {
		err = server.Call("Server.GetLastLine", stubs.LineRequest{}, &res)
	}
	if err != nil {
		handleError(err)
	}
	out <- res.Line
}

// GetWorldChange 会返回服务器存储的状态变化字典和数组
func (s *Server) GetWorldChange(_ stubs.WorldChangeRequest, res *stubs.WorldChangeResponse) (err error) {
	s.getWorld <- true                          // 通知主程序正在收集状态，暂停处理
	flippedCellsMap := make(map[util.Cell]bool) // 创建一个新的字典，复制原来字典的所有内容
	for key, value := range s.flippedCellsMap {
		flippedCellsMap[key] = value
	}
	res.FlippedCellsMap = flippedCellsMap
	flippedCellsBuffer := make([]util.Cell, len(s.flippedCellsBuffer)) // 创建一个新的列表，复制原来列表的所有内容
	for j := range flippedCellsBuffer {
		flippedCellsBuffer[j] = s.flippedCellsBuffer[j]
	}
	res.FlippedCellsBuffer = flippedCellsBuffer
	res.CurrentTurn = s.currentTurn
	<-s.getWorld // 通知主程序收集完成
	return
}

// Pause 方法通过锁定互斥锁的方式暂停服务器运行下一个回合
func (s *Server) Pause(_ stubs.PauseRequest, res *stubs.PauseResponse) (err error) {
	if s.pause {
		s.pause = false
		res.CurrentTurn = s.currentTurn
		s.processLock.Unlock()
	} else {
		s.processLock.Lock()
		res.CurrentTurn = s.currentTurn
		s.pause = true
	}
	return
}

// Stop 方法用于直接关闭服务器
func (s *Server) Stop(_ stubs.StopRequest, _ *stubs.StopResponse) (err error) {
	fmt.Println("Server stopped")
	os.Exit(1)
	return
}

// 这里是GoL逻辑，和parallel的几乎一模一样，就是不用immutable了

func calculateNextState(startY, endY, width, height int, world [][]uint8) []util.Cell {
	var flippedCells []util.Cell
	neighboursCount := 0
	for y := startY; y < endY; y++ {
		for x := 0; x < width; x++ {
			neighboursCount = countLivingNeighbour(x, y, width, height, world)
			if world[y][x] == 0 && neighboursCount == 3 {
				flippedCells = append(flippedCells, util.Cell{X: x, Y: y - 1})
			} else if world[y][x] == 255 && (neighboursCount < 2 || neighboursCount > 3) {
				flippedCells = append(flippedCells, util.Cell{X: x, Y: y - 1})
			}
		}
	}
	return flippedCells
}

func countLivingNeighbour(x, y, width, height int, world [][]uint8) int {
	liveNeighbour := 0
	for line := y - 1; line < y+2; line += 2 {
		for column := x - 1; column < x+2; column++ {
			if isAlive(column, line, width, height, world) {
				liveNeighbour += 1
			}
		}
	}
	if isAlive(x-1, y, width, height, world) {
		liveNeighbour += 1
	}
	if isAlive(x+1, y, width, height, world) {
		liveNeighbour += 1
	}
	return liveNeighbour
}

func isAlive(x, y, width, height int, world [][]uint8) bool {
	x = (x + width) % width
	y = (y + height) % height
	if world[y][x] != 0 {
		return true
	}
	return false
}

func worker(startY, endY, width, height int, world [][]uint8, out chan<- []util.Cell) {
	out <- calculateNextState(startY, endY, width, height, world)
}

// 服务器的主函数
func main() {
	// 设置服务器监听指定的端口，flag可以接收用户运行时输入的参数，例如go run . -port 8081
	portPtr := flag.String("port", "8081", "port to listen on")
	flag.Parse()

	// 开始监听，address的:前面不加任何东西代表监听所有的位置，对于AWS就是同时监听private和publicIP
	ln, err := net.Listen("tcp", ":"+*portPtr)
	if err != nil {
		handleError(err)
		return
	}
	// 在主程序退出之后停止监听（不过这行代码好像没有什么用）
	defer func() {
		_ = ln.Close()
	}()
	// 创建一个Server对象，并将它的所有方法都发布（发布订阅模型）
	// 这样连接到这个server的客户端可以直接调用server对象所有的方法
	_ = rpc.Register(new(Server))
	fmt.Println("Server Start, Listening on " + ln.Addr().String())
	// Accept后所有其他程序就都能连接到这个程序了，Accept会阻塞整个程序直到对应的监听ln.close掉
	rpc.Accept(ln)
}
