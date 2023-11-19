package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"sync"
	"uk.ac.bris.cs/gameoflife/stubs"
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
	// 结束服务器当前的Gol并开始新的Gol
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
		var outChannels []chan [][]uint8
		currentHeight := 0
		for i := 0; i < req.Threads; i++ {
			size = averageHeight
			// 将除不尽的部分分配到前几个threads中，每个threads一行
			if i < restHeight {
				size += 1
			}
			outChannel := make(chan [][]uint8)
			outChannels = append(outChannels, outChannel)
			//r(startY, endY, width, height int, world [][]uint8, out chan<- [][]uint8)
			go worker(currentHeight, currentHeight+size, req.GolBoard.Width, req.GolBoard.Height, world, outChannel)
			currentHeight += size
		}

		var worldPart [][]uint8
		var newWorld [][]uint8
		for i := 0; i < req.Threads; i++ {
			worldPart = <-outChannels[i]
			for _, linePart := range worldPart {
				newWorld = append(newWorld, linePart)
			}
		}
		s.processLock.Lock()
		world = newWorld
		s.world = newWorld
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
	return
}

// CountAliveCells 补充注释
func (s *Server) CountAliveCells(req stubs.AliveCellsCountRequest, res *stubs.AliveCellsCountResponse) (err error) {
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

func (s *Server) GetWorld(req stubs.CurrentWorldRequest, res *stubs.CurrentWorldResponse) (err error) {
	s.processLock.Lock()
	res.GolBoard = stubs.GolBoard{World: s.world, CurrentTurn: s.currentTurn, Width: s.worldWidth, Height: s.worldHeight}
	s.processLock.Unlock()
	return
}

func (s *Server) Pause(req stubs.PauseRequest, res *stubs.PauseResponse) (err error) {
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

func (s *Server) Stop(req stubs.StopRequest, res *stubs.StopResponse) (err error) {
	fmt.Println("Server stopped")
	os.Exit(0)
	return
}

/*func test(req stubs.TestReq, res stubs.TestRes) {
	res.Value = req.Value + 1
	return
}*/

func main() {
	portPtr := flag.String("port", ":8080", "port to listen on")
	flag.Parse()

	ln, err := net.Listen("tcp", *portPtr)
	if err != nil {
		handleError(err)
		return
	}
	defer ln.Close()
	rpc.Register(new(Server))
	fmt.Println("Server Start, Listening on 8080")
	rpc.Accept(ln)
}

// build 接收长度和宽度并生成一个指定长度x宽度的2D矩阵
func build(height, width int) [][]uint8 {
	newMatrix := make([][]uint8, height)
	for i := range newMatrix {
		newMatrix[i] = make([]uint8, width)
	}
	return newMatrix
}

// calculateNextState 会计算以startY列开始，endY-1列结束的世界的下一步的状态
func calculateNextState(startY, endY, width, height int, world [][]uint8) [][]uint8 {
	worldNextState := build(endY-startY, width)
	// 将要处理的world部分的数据映射到worldNextState上
	for y := startY; y < endY; y++ {
		for x := 0; x < width; x++ {
			worldNextState[y-startY][x] = world[y][x] // worldNextState的坐标系从y=0开始
		}
	}
	// 计算每个点周围的邻居并将状态写入worldNextState
	neighboursCount := 0
	for y := startY; y < endY; y++ {
		for x := 0; x < width; x++ {
			neighboursCount = countLivingNeighbour(x, y, width, height, world)
			if world[y][x] == 0 && neighboursCount == 3 { // 死亡的细胞邻居刚好为3个时复活
				worldNextState[y-startY][x] = 255
			} else if world[y][x] == 255 && (neighboursCount < 2 || neighboursCount > 3) { // 存活的细胞邻居少于2个或多于3个时死亡
				worldNextState[y-startY][x] = 0
			}
		}
	}
	return worldNextState

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
func worker(startY, endY, width, height int, world [][]uint8, out chan<- [][]uint8) {
	out <- calculateNextState(startY, endY, width, height, world)
}
