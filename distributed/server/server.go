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

type Server struct {
	world          [][]uint8
	height         int
	width          int
	threads        int
	working        bool
	quit           chan bool
	firstLineSent  chan bool
	lastLineSent   chan bool
	previousServer *rpc.Client
	nextServer     *rpc.Client
}

func handleError(err error) {
	fmt.Println("Error:", err)
	os.Exit(1)
}

// worldCreate 将晕区和世界结合生成一个新的世界
func worldCreate(height int, world [][]uint8, upperHalo, downerHalo []uint8) [][]uint8 {
	newWorld := make([][]uint8, 0, height+2)
	newWorld = append(newWorld, upperHalo)
	newWorld = append(newWorld, world...)
	newWorld = append(newWorld, downerHalo)
	return newWorld
}

func (s *Server) Init(req stubs.InitRequest, _ *stubs.InitResponse) (err error) {
	if s.working {
		s.quit <- true
	}
	s.quit = make(chan bool)
	s.firstLineSent = make(chan bool)
	s.lastLineSent = make(chan bool)
	s.world = req.GolBoard.World
	s.threads = req.Threads
	s.width = req.GolBoard.Width
	s.height = req.GolBoard.Height
	s.previousServer, _ = rpc.Dial("tcp", req.PreviousServer.Address+":"+req.PreviousServer.Port)
	fmt.Println("Connect to previous halo server ", req.PreviousServer.Address+":"+req.PreviousServer.Port)
	s.nextServer, _ = rpc.Dial("tcp", req.NextServer.Address+":"+req.NextServer.Port)
	fmt.Println("Connect to next halo server ", req.NextServer.Address+":"+req.NextServer.Port)
	return
}

func (s *Server) GetFirstLine(_ stubs.LineRequest, res *stubs.LineResponse) (err error) {
	line := make([]uint8, len(s.world[0]))
	for i, value := range s.world[0] {
		line[i] = value
	}
	res.Line = line
	s.firstLineSent <- true
	return
}

func (s *Server) GetLastLine(_ stubs.LineRequest, res *stubs.LineResponse) (err error) {
	line := make([]uint8, len(s.world[s.height-1]))
	for i, value := range s.world[s.height-1] {
		line[i] = value
	}
	res.Line = line
	s.lastLineSent <- true
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

func (s *Server) NextTurn(_ stubs.NextTurnRequest, res *stubs.NextTurnResponse) (err error) {
	s.working = true
	upperOut := make(chan []uint8)
	nextOut := make(chan []uint8)

	go getHalo(s.previousServer, false, upperOut)
	go getHalo(s.nextServer, true, nextOut)

	<-s.firstLineSent
	<-s.lastLineSent

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
			// 将除不尽的部分分配到前几个threads中，每个threads一行
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
	select {
	case <-s.quit:
		break
	default:
		break
	}
	s.working = false
	return
}

func (s *Server) Stop(_ stubs.StopRequest, _ *stubs.StopResponse) (err error) {
	fmt.Println("Server stopped")
	os.Exit(1)
	return
}

// calculateNextState 会计算以startY列开始，endY-1列结束的世界的下一步的状态
func calculateNextState(startY, endY, width, height int, world [][]uint8) []util.Cell {
	// 计算所有需要改变的细胞
	var flippedCells []util.Cell
	// 计算每个点周围的邻居并将状态写入worldNextState
	neighboursCount := 0
	for y := startY; y < endY; y++ {
		for x := 0; x < width; x++ {
			neighboursCount = countLivingNeighbour(x, y, width, height, world)
			if world[y][x] == 0 && neighboursCount == 3 { // 死亡的细胞邻居刚好为3个时复活
				flippedCells = append(flippedCells, util.Cell{X: x, Y: y - 1})
			} else if world[y][x] == 255 && (neighboursCount < 2 || neighboursCount > 3) { // 存活的细胞邻居少于2个或多于3个时死亡
				flippedCells = append(flippedCells, util.Cell{X: x, Y: y - 1})
			}
		}
	}
	return flippedCells
}

// countLivingNeighbour 通过调用 isAlive 函数判断一个节点有多少存活的邻居，返回存活邻居的数量
func countLivingNeighbour(x, y, width, height int, world [][]uint8) int {
	liveNeighbour := 0
	for line := y - 1; line < y+2; line += 2 { // 判断前一行和后一行
		for column := x - 1; column < x+2; column++ { // 判断该行3个邻居是否存活
			if isAlive(column, line, width, height, world) {
				liveNeighbour += 1
			}
		}
	}
	// 判断左右边的邻居是否存活
	if isAlive(x-1, y, width, height, world) {
		liveNeighbour += 1
	}
	if isAlive(x+1, y, width, height, world) {
		liveNeighbour += 1
	}
	return liveNeighbour
}

// isAlive 判断一个节点是否存活，支持超出边界的节点判断（上方超界则判断最后一行，左方超界则判断最后一列，以此类推）
func isAlive(x, y, width, height int, world [][]uint8) bool {
	x = (x + width) % width
	y = (y + height) % height
	if world[y][x] != 0 {
		return true
	}
	return false
}

// 将任务分配到每个线程
func worker(startY, endY, width, height int, world [][]uint8, out chan<- []util.Cell) {
	out <- calculateNextState(startY, endY, width, height, world)
}

func main() {
	portPtr := flag.String("port", "8081", "port to listen on")
	flag.Parse()

	ln, err := net.Listen("tcp", ":"+*portPtr)
	if err != nil {
		handleError(err)
		return
	}
	defer func() {
		_ = ln.Close()
	}()
	_ = rpc.Register(new(Server))
	fmt.Println("Server Start, Listening on " + ln.Addr().String())
	rpc.Accept(ln)
}
