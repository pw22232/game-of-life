package gol

import (
	"strconv"
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

func build(height, width int) [][]uint8 {
	newMatrix := make([][]uint8, height)
	for i := range newMatrix {
		newMatrix[i] = make([]uint8, width)
	}
	return newMatrix
}

// makeImmutableWorld takes an existing gol world and make sure no one can change it.
func makeImmutableWorld(world [][]uint8) func(y, x int) uint8 {
	return func(y, x int) uint8 {
		return world[y][x]
	}
}

func calculateAliveCells(p Params, world [][]uint8) []util.Cell {
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

func calculateNextState(startY, endY int, p Params, world func(y, x int) uint8) [][]uint8 {
	worldNextState := build(endY-startY-1, p.ImageWidth)
	for y := startY + 1; y < endY; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			worldNextState[y-startY-1][x] = world(y, x)
		}
	}
	neighboursCount := 0
	for yIndex := startY + 1; yIndex < endY; yIndex++ {
		for xIndex := 0; xIndex < p.ImageWidth; xIndex++ {
			neighboursCount = countLivingNeighbour(xIndex, yIndex, world, p)
			if world(yIndex, xIndex) == 0 {
				if neighboursCount == 3 { // 如果该节点当前为死亡状态，并且有三个邻居，则复活该节点
					worldNextState[yIndex-startY-1][xIndex] = 255
				}
			} else {
				if neighboursCount < 2 || neighboursCount > 3 { // 存活的节点邻居少于2个或多于3个则杀死该节点
					worldNextState[yIndex-startY-1][xIndex] = 0
				}
			}
		}

	}
	return worldNextState
}

// 判断一个节点有多少存活的邻居，会调用isAlive函数，返回存活邻居的数量
func countLivingNeighbour(x, y int, world func(y, x int) uint8, p Params) int {
	liveNeighbour := 0
	for line := y - 1; line < y+2; line += 2 { // 判断前一行和后一行
		for column := x - 1; column < x+2; column++ { // 判断该行3个邻居是否存活
			if isAlive(column, line, world, p) {
				liveNeighbour += 1
			}
		}
	}
	// 判断左右边的邻居是否存活
	if isAlive(x-1, y, world, p) {
		liveNeighbour += 1
	}
	if isAlive(x+1, y, world, p) {
		liveNeighbour += 1
	}
	return liveNeighbour
}

// 判断一个节点是否存活，支持超出边界的节点判断（上方超界则判断最后一行，左方超界则判断最后一列，以此类推）
func isAlive(x, y int, world func(y, x int) uint8, p Params) bool {
	if x < 0 {
		x = p.ImageWidth - 1
	}
	if x > p.ImageWidth-1 {
		x = 0
	}
	if y < 0 {
		y = p.ImageHeight - 1
	}
	if y > p.ImageHeight-1 {
		y = 0
	}
	if world(y, x) == 255 {
		return true
	}
	return false
}

func worker(startY, endY int, p Params, data func(y, x int) uint8, out chan<- [][]uint8) {
	out <- calculateNextState(startY, endY, p, data)
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {

	// TODO: Create a 2D slice to store the world.
	world := build(p.ImageHeight, p.ImageWidth)

	turn := 0
	c.ioCommand <- ioInput
	c.ioFilename <- strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.ImageWidth)

	for i := 0; i < p.ImageHeight; i++ {
		for j := 0; j < p.ImageWidth; j++ {
			value := <-c.ioInput
			world[i][j] = value
		}
	}

	for currentTurn := 0; currentTurn < p.Turns; currentTurn++ {
		immutableWorld := makeImmutableWorld(world)
		var outChannel []chan [][]uint8
		averageHeight := p.ImageHeight / p.Threads
		restHeight := p.ImageHeight % p.Threads
		currentHeight := 0
		size := averageHeight
		for i := 0; i < p.Threads; i++ {
			size = averageHeight
			if i < restHeight {
				size += 1
			}
			channel := make(chan [][]uint8)
			outChannel = append(outChannel, channel)
			go worker(currentHeight-1, currentHeight+size, p, immutableWorld, channel)
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
		world = newWorld
	}

	// TODO: Execute all turns of the Game of Life.

	// TODO: Report the final state using FinalTurnCompleteEvent.

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}
	c.events <- FinalTurnComplete{turn, calculateAliveCells(p, world)}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
