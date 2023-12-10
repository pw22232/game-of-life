package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

// Server 类型保存服务器所有的属性，服务器作为一个对象（面对对象）
type Server struct {
	world          [][]uint8 // 2D数组，世界
	height         int       // 高度，宽度，线程
	width          int
	threads        int
	working        bool      // 服务器是否正在运行
	quit           chan bool // 用户按k时通知服务器关闭的通道
	firstLineSent  chan bool // 检测是否已经发送上下光环的通道
	lastLineSent   chan bool
	previousServer *rpc.Client // 自己的上下光环服务器rpc，这里保存的是rpc客户端的pointer，
	nextServer     *rpc.Client // 这样就不用每次获取光环时都需要连接服务器了（但是这样就没法做容错了）
}

// handleError 在发生错误时输出错误并退出程序
func handleError(err error) {
	fmt.Println("Error:", err)
	os.Exit(1)
}

// 将自己的世界和上下两行获取到的光环组合起来成为一个新的世界
func worldCreate(height int, world [][]uint8, upperHalo, downerHalo []uint8) [][]uint8 {
	newWorld := make([][]uint8, 0, height+2)
	newWorld = append(newWorld, upperHalo)
	newWorld = append(newWorld, world...)
	newWorld = append(newWorld, downerHalo)
	return newWorld
}

// Init 是服务器初始化函数，初始化时会结束之前在运行的操作，然后重新设置自己的属性
func (s *Server) Init(req stubs.InitRequest, _ *stubs.InitResponse) (err error) {
	if s.working { // 如果服务器正在运行一回合，为了防止出问题，先用通道阻塞，等待服务器结束这一回合
		s.quit <- true
	}
	// 重新创建所有通道
	s.quit = make(chan bool)
	s.firstLineSent = make(chan bool)
	s.lastLineSent = make(chan bool)
	// 将获取到的棋盘数据保存到服务器的属性里
	s.world = req.GolBoard.World
	s.threads = req.Threads
	s.width = req.GolBoard.Width
	s.height = req.GolBoard.Height
	// 连接光环服务器，并将连接后的输出保存到属性里（后面调用就不用再连接了）
	s.previousServer, _ = rpc.Dial("tcp", req.PreviousServer.Address+":"+req.PreviousServer.Port)
	fmt.Println("Connect to previous halo server ", req.PreviousServer.Address+":"+req.PreviousServer.Port)
	s.nextServer, _ = rpc.Dial("tcp", req.NextServer.Address+":"+req.NextServer.Port)
	fmt.Println("Connect to next halo server ", req.NextServer.Address+":"+req.NextServer.Port)
	return
}

// 这两个函数用于获取光环，其实写成一个函数会更好，因为内容重复我就不写两遍注释了

// GetFirstLine 允许其他服务器调用，调用时会返回自己世界第一行的数据，完成后向通道传递信息
func (s *Server) GetFirstLine(_ stubs.LineRequest, res *stubs.LineResponse) (err error) {
	// 这里不用互斥锁的原因是服务器在交换光环的过程中是阻塞的，不会修改世界的数据
	line := make([]uint8, len(s.world[0])) // 创建一个长度和世界第一行相同的列表（其实这里直接用s.width会更好）
	for i, value := range s.world[0] {
		line[i] = value // 将世界第一行每个值复制进新的数组（这样即使世界被修改光环也肯定不会变）
	}
	res.Line = line
	s.firstLineSent <- true // 在交换前向通道传递值，这样保证所有服务器都完成光环交换后再继续运行下回合
	return
}

// GetLastLine 返回自己世界最后一行的数据，和 GetFirstLine 逻辑相同
func (s *Server) GetLastLine(_ stubs.LineRequest, res *stubs.LineResponse) (err error) {
	line := make([]uint8, len(s.world[s.height-1]))
	for i, value := range s.world[s.height-1] {
		line[i] = value
	}
	res.Line = line
	s.lastLineSent <- true
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

func (s *Server) NextTurn(_ stubs.NextTurnRequest, res *stubs.NextTurnResponse) (err error) {
	s.working = true // 开始工作时将working状态设置为true，防止服务器在运行回合时属性被修改
	// 这两个通道用于接收getHalo返回的光环区域
	upperOut := make(chan []uint8)
	nextOut := make(chan []uint8)
	// 所有服务器都在子线程请求其他服务器输出光环，这样就不会发生死锁了
	go getHalo(s.previousServer, false, upperOut)
	go getHalo(s.nextServer, true, nextOut)
	// 等待自己相邻的服务器向自己请求光环
	<-s.firstLineSent
	<-s.lastLineSent
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
	res.FlippedCells = flippedCells
	for _, flippedCell := range flippedCells {
		if s.world[flippedCell.Y][flippedCell.X] == 255 {
			s.world[flippedCell.Y][flippedCell.X] = 0
		} else {
			s.world[flippedCell.Y][flippedCell.X] = 255
		}
	}
	// 如果服务器当前准备重新初始化了，这里就会直接解除阻塞，这样服务器就可以重置了
	select {
	case <-s.quit:
		break
	default:
		break
	}
	s.working = false // 因为这个版本是每回合server和broker都同步的，所以回合结束直接设置working为false
	// 其实working=false应该放在那个select前面，放在这里有极小的概率在服务器重置时导致服务器死锁
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
