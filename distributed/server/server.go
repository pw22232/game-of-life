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
	world       [][]uint8
	worldWidth  int
	worldHeight int
	currentTurn int
	working     bool
	paused      bool
	processLock sync.Mutex
	quit        chan bool
}

func handleError(err error) {
	fmt.Println("Error:", err)
}

// RunGol distributor divides the work between workers and interacts with other goroutines.
func (s *Server) RunGol(req stubs.RunGolRequest, res *stubs.RunGolResponse) (err error) {
	//结束服务器当前的Gol并开始新的Gol
	if s.paused {
		s.processLock.Unlock()
	}
	if s.working {
		s.quit <- true
	}
	s.working = true
	s.quit = make(chan bool)
	// 根据需要处理的回合数量进行循环
	world := req.GolBoard.World
	turn := 0
	s.processLock.Lock()
	s.currentTurn = 0
	s.world = req.GolBoard.World
	s.worldHeight = req.GolBoard.Height
	s.worldWidth = req.GolBoard.Width
	s.processLock.Unlock()
	averageHeight := req.GolBoard.Height / req.Threads
	restHeight := req.GolBoard.Height % req.Threads
	size := averageHeight

	for turn = 0; turn < req.Turns; turn++ {
		var outChannels []chan []util.Cell
		currentHeight := 0
		for i := 0; i < req.Threads; i++ {
			size = averageHeight
			if i < restHeight {
				// 将除不尽的部分分配到前几个threads中，每个threads一行
				size += 1
			}
			outChannel := make(chan []util.Cell)
			outChannels = append(outChannels, outChannel)
			go worker(currentHeight, currentHeight+size, req.GolBoard.Width, req.GolBoard.Height, world, outChannel)
			currentHeight += size
		}
		var flippedCells []util.Cell
		for i := 0; i < req.Threads; i++ {
			flippedCells = append(flippedCells, <-outChannels[i]...)
		}
		for _, flippedCell := range flippedCells {
			if world[flippedCell.Y][flippedCell.X] == 255 {
				world[flippedCell.Y][flippedCell.X] = 0
			} else {
				world[flippedCell.Y][flippedCell.X] = 255
			}
		}
		s.processLock.Lock()
		s.world = world
		s.currentTurn = turn + 1
		s.processLock.Unlock()

		select {
		case <-s.quit:
			return
		default:
			break
		}
	}
	res.GolBoard = stubs.GolBoard{World: world, CurrentTurn: turn}
	s.working = false
	return
}

// CountAliveCells 补充注释
func (s *Server) CountAliveCells(_ stubs.AliveCellsCountRequest, res *stubs.AliveCellsCountResponse) (err error) {
	aliveCellsCount := 0
	s.processLock.Lock()
	for x := 0; x < s.worldWidth; x++ {
		for y := 0; y < s.worldHeight; y++ {
			if s.world[y][x] == 255 {
				aliveCellsCount++
			}
		}
	}
	res.Count = aliveCellsCount
	res.CurrentTurn = s.currentTurn
	s.processLock.Unlock()
	return
}

func (s *Server) GetWorld(_ stubs.CurrentWorldRequest, res *stubs.CurrentWorldResponse) (err error) {
	s.processLock.Lock()
	res.GolBoard = stubs.GolBoard{World: s.world, CurrentTurn: s.currentTurn, Width: s.worldWidth, Height: s.worldHeight}
	s.processLock.Unlock()
	return
}

func (s *Server) Pause(_ stubs.PauseRequest, res *stubs.PauseResponse) (err error) {
	if s.paused {
		s.processLock.Unlock()
		s.paused = false
	} else {
		s.processLock.Lock()
		s.paused = true
	}
	res.CurrentTurn = s.currentTurn
	return
}

func (s *Server) Stop(_ stubs.StopRequest, _ *stubs.StopResponse) (err error) {
	fmt.Println("Server stopped")
	os.Exit(0)
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
				flippedCells = append(flippedCells, util.Cell{X: x, Y: y})
			} else if world[y][x] == 255 && (neighboursCount < 2 || neighboursCount > 3) { // 存活的细胞邻居少于2个或多于3个时死亡
				flippedCells = append(flippedCells, util.Cell{X: x, Y: y})
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
	portPtr := flag.String("port", "8080", "port to listen on")
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
