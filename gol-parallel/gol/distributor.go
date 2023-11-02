package gol

import (
	"strconv"
	"sync"
	"time"
	"uk.ac.bris.cs/gameoflife/util"
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
}

// build 接收长度和宽度并生成一个指定长度x宽度的2D矩阵
func build(height, width int) [][]uint8 {
	newMatrix := make([][]uint8, height)
	for i := range newMatrix {
		newMatrix[i] = make([]uint8, width)
	}
	return newMatrix
}

// makeImmutableWorld 将指定的世界转换为函数，转换后只能被读取，不能被修改
func makeImmutableWorld(world [][]uint8) func(y, x int) uint8 {
	return func(y, x int) uint8 {
		return world[y][x]
	}
}

// findAliveCells 返回世界中所有存活的细胞
func findAliveCells(p Params, world [][]uint8) []util.Cell {
	var aliveCells []util.Cell
	for x := 0; x < p.ImageWidth; x++ {
		for y := 0; y < p.ImageHeight; y++ {
			if world[y][x] == 255 {
				aliveCells = append(aliveCells, util.Cell{X: x, Y: y})
			}
		}
	}
	return aliveCells
}

func countAliveCells(p Params, immutableWord func(y, x int) uint8) int {
	aliveCellsCount := 0
	for x := 0; x < p.ImageWidth; x++ {
		for y := 0; y < p.ImageHeight; y++ {
			if immutableWord(y, x) == 255 {
				aliveCellsCount++
			}
		}
	}
	return aliveCellsCount
}

// calculateNextState 会计算以startY列开始，endY-1列结束的世界的下一步的状态
func calculateNextState(startY, endY, width, height int, immutableWorld func(y, x int) uint8) [][]uint8 {
	worldNextState := build(endY-startY, width)
	// 将要处理的world部分的数据映射到worldNextState上
	for y := startY; y < endY; y++ {
		for x := 0; x < width; x++ {
			worldNextState[y-startY][x] = immutableWorld(y, x) // worldNextState的坐标系从y=0开始
		}
	}
	// 计算每个点周围的邻居并将状态写入worldNextState
	neighboursCount := 0
	for y := startY; y < endY; y++ {
		for x := 0; x < width; x++ {
			neighboursCount = countLivingNeighbour(x, y, width, height, immutableWorld)
			if immutableWorld(y, x) == 0 && neighboursCount == 3 { // 死亡的细胞邻居刚好为3个时复活
				worldNextState[y-startY][x] = 255
			} else if neighboursCount < 2 || neighboursCount > 3 { // 存活的细胞邻居少于2个或多于3个时死亡
				worldNextState[y-startY][x] = 0
			}
		}

	}
	return worldNextState
}

// countLivingNeighbour 通过调用 isAlive 函数判断一个节点有多少存活的邻居，返回存活邻居的数量
func countLivingNeighbour(x, y, width, height int, immutableWorld func(y, x int) uint8) int {
	liveNeighbour := 0
	for line := y - 1; line < y+2; line += 2 { // 判断前一行和后一行
		for column := x - 1; column < x+2; column++ { // 判断该行3个邻居是否存活
			if isAlive(column, line, width, height, immutableWorld) {
				liveNeighbour += 1
			}
		}
	}
	// 判断左右边的邻居是否存活
	if isAlive(x-1, y, width, height, immutableWorld) {
		liveNeighbour += 1
	}
	if isAlive(x+1, y, width, height, immutableWorld) {
		liveNeighbour += 1
	}
	return liveNeighbour
}

// 判断一个节点是否存活，支持超出边界的节点判断（上方超界则判断最后一行，左方超界则判断最后一列，以此类推）
func isAlive(x, y, width, height int, immutableWorld func(y, x int) uint8) bool {
	x = (x + width) % width
	y = (y + height) % height
	if immutableWorld(y, x) != 0 {
		return true
	}
	return false
}

// 将任务分配到每个线程
func worker(startY, endY int, p Params, immutableWorld func(y, x int) uint8, out chan<- [][]uint8) {
	out <- calculateNextState(startY, endY, p.ImageWidth, p.ImageHeight, immutableWorld)
}

func timer(ticker *time.Ticker, events chan<- Event, worldLock *sync.Mutex, turn *int, p Params, immutableWorld *func(y, x int) uint8) {
	for {
		<-ticker.C
		worldLock.Lock()
		events <- AliveCellsCount{CompletedTurns: *turn, CellsCount: countAliveCells(p, *immutableWorld)}
		worldLock.Unlock()
	}
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {
	var worldLock sync.Mutex

	// TODO: Create a 2D slice to store the world.
	world := build(p.ImageHeight, p.ImageWidth)

	turn := 0
	c.ioCommand <- ioInput
	c.ioFilename <- strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.ImageWidth)

	// 初始化世界，ioInput管道会每次传递一个值，从世界的左上角到右下角
	for i := 0; i < p.ImageHeight; i++ {
		for j := 0; j < p.ImageWidth; j++ {
			value := <-c.ioInput
			world[i][j] = value
		}
	}

	immutableWorld := makeImmutableWorld(world)
	ticker := time.NewTicker(1 * time.Second)
	go timer(ticker, c.events, &worldLock, &turn, p, &immutableWorld)
	// 根据需要处理的回合数量进行循环
	for turn = 0; turn < p.Turns; {
		// 将世界转换为函数来防止其被意外修改
		immutableWorld = makeImmutableWorld(world)
		var outChannel []chan [][]uint8
		averageHeight := p.ImageHeight / p.Threads
		restHeight := p.ImageHeight % p.Threads
		currentHeight := 0
		size := averageHeight
		for i := 0; i < p.Threads; i++ {
			size = averageHeight
			// 将除不尽的部分分配到前几个threads中，每个threads一行
			if i < restHeight {
				size += 1
			}
			channel := make(chan [][]uint8)
			outChannel = append(outChannel, channel)
			go worker(currentHeight, currentHeight+size, p, immutableWorld, channel)
			currentHeight += size
		}

		currentLineIndex := 0
		var worldPart [][]uint8
		var newWorld [][]uint8
		for i := 0; i < p.Threads; i++ {
			worldPart = <-outChannel[i]
			for _, linePart := range worldPart {
				newWorld = append(newWorld, linePart)
			}
			currentLineIndex += len(worldPart)
		}
		worldLock.Lock()
		world = newWorld
		c.events <- TurnComplete{CompletedTurns: turn}
		turn++
		worldLock.Unlock()
	}

	// TODO: Execute all turns of the Game of Life.

	// TODO: Report the final state using FinalTurnCompleteEvent.

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	ticker.Stop()
	c.events <- StateChange{turn, Quitting}
	c.events <- FinalTurnComplete{turn, findAliveCells(p, world)}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
