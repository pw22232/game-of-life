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

type Server struct {
	world              [][]uint8
	flippedCellsMap    map[util.Cell]bool
	flippedCellsBuffer []util.Cell
	startY             int
	height             int
	width              int
	threads            int
	turns              int
	currentTurn        int
	working            bool
	pause              bool
	quit               chan bool
	firstLineReady     chan bool
	lastLineReady      chan bool
	getWorld           chan bool
	previousServer     *rpc.Client
	nextServer         *rpc.Client
	processLock        sync.Mutex
}

func handleError(err error) {
	fmt.Println("Error:", err)
}

func worldCreate(height int, world [][]uint8, upperHalo, downerHalo []uint8) [][]uint8 {
	newWorld := make([][]uint8, 0, height+2)
	newWorld = append(newWorld, upperHalo)
	newWorld = append(newWorld, world...)
	newWorld = append(newWorld, downerHalo)
	return newWorld
}

func (s *Server) InitServer(req stubs.InitServerRequest, _ *stubs.InitServerResponse) (err error) {
	if s.pause {
		s.processLock.Unlock()
	}
	if s.working {
		s.quit <- true
	}
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

func (s *Server) RunServer(_ stubs.RunServerRequest, res *stubs.RunServerResponse) (err error) {
	s.processLock.Lock()
	turn := s.currentTurn
	turns := s.turns
	s.working = true
	s.processLock.Unlock()
	for turn < turns {
		upperOut := make(chan []uint8)
		nextOut := make(chan []uint8)
		go getHalo(s.previousServer, false, upperOut)
		go getHalo(s.nextServer, true, nextOut)
		s.firstLineReady <- true
		s.lastLineReady <- true
		select {
		case <-s.getWorld:
			s.getWorld <- true
		default:
			break
		}
		<-s.firstLineReady
		<-s.lastLineReady
		upperHalo := <-upperOut
		nextHalo := <-nextOut
		world := worldCreate(s.height, s.world, upperHalo, nextHalo)

		var outChannels []chan []util.Cell
		currentHeight := 1
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
		for _, flippedCell := range s.flippedCellsBuffer {
			if s.flippedCellsMap[flippedCell] {
				delete(s.flippedCellsMap, flippedCell)
			} else {
				s.flippedCellsMap[flippedCell] = true
			}
		}
		for i := range flippedCells {
			flippedCells[i].Y += s.startY
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
	s.processLock.Lock()
	res.World = s.world
	s.working = false
	s.processLock.Unlock()
	return
}

func (s *Server) GetFirstLine(_ stubs.LineRequest, res *stubs.LineResponse) (err error) {
	<-s.firstLineReady

	s.processLock.Lock()
	line := make([]uint8, len(s.world[0]))
	for i, value := range s.world[0] {
		line[i] = value
	}
	res.Line = line
	s.processLock.Unlock()
	s.firstLineReady <- true
	return
}

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

func (s *Server) GetWorldChange(_ stubs.WorldChangeRequest, res *stubs.WorldChangeResponse) (err error) {
	s.getWorld <- true
	flippedCellsMap := make(map[util.Cell]bool)
	for key, value := range s.flippedCellsMap {
		flippedCellsMap[key] = value
	}
	res.FlippedCellsMap = flippedCellsMap
	flippedCellsBuffer := make([]util.Cell, len(s.flippedCellsBuffer))
	for j := range flippedCellsBuffer {
		flippedCellsBuffer[j] = s.flippedCellsBuffer[j]
	}
	res.FlippedCellsBuffer = flippedCellsBuffer
	res.CurrentTurn = s.currentTurn
	<-s.getWorld
	return
}

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

func (s *Server) Stop(_ stubs.StopRequest, _ *stubs.StopResponse) (err error) {
	fmt.Println("Server stopped")
	os.Exit(1)
	return
}

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
