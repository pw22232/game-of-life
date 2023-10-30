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

func calculateNextState(p Params, world [][]uint8) [][]uint8 {
	var changeAliveCells []util.Cell
	var changeDieCells []util.Cell
	neighboursCount := 0
	for yIndex := range world {
		for xIndex := range world[yIndex] {
			neighboursCount = countLivingNeighbour(xIndex, yIndex, world, p)
			if world[yIndex][xIndex] == 0 {
				if neighboursCount == 3 { // 如果该节点当前为死亡状态，并且有三个邻居，则标记为复活
					changeAliveCells = append(changeAliveCells, util.Cell{xIndex, yIndex})
				}
			} else {
				if neighboursCount < 2 || neighboursCount > 3 { // 存活的节点邻居少于2个或多于3个则标记为濒死
					changeDieCells = append(changeDieCells, util.Cell{xIndex, yIndex})
				}
			}
		}

	}
	// 结算复活和濒死的节点并在世界上做出改变
	for _, cells := range changeAliveCells {
		world[cells.Y][cells.X] = 255
	}
	for _, cells := range changeDieCells {
		world[cells.Y][cells.X] = 0
	}
	return world
}

// 判断一个节点有多少存活的邻居，会调用isAlive函数，返回存活邻居的数量
func countLivingNeighbour(x int, y int, world [][]uint8, p Params) int {
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
func isAlive(x int, y int, world [][]uint8, p Params) bool {
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
	if world[y][x] == 255 {
		return true
	}
	return false
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {

	// TODO: Create a 2D slice to store the world.
	world := build(p.ImageHeight, p.ImageWidth)
	final := build(p.ImageHeight, p.ImageWidth)

	turn := 0
	c.ioCommand <- ioInput
	c.ioFilename <- strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.ImageWidth)

	for i := 0; i < p.ImageHeight; i++ {
		for j := 0; j < p.ImageWidth; j++ {
			value := <-c.ioInput
			world[i][j] = value
		}
	}

	/*	f := calculateAliveCells(p, world)
		for _, v := range f {
			c.events <- CellFlipped{0, v}
		}
	*/
	if p.Threads == 1 {
		for i := 0; i < p.Turns; i++ {
			temp := build(p.ImageHeight, p.ImageWidth)
			temp = calculateNextState(p, world)
			world = temp
			c.events <- TurnComplete{i + 1}
			turn++
		}
		final = world
	}

	// TODO: Execute all turns of the Game of Life.

	// TODO: Report the final state using FinalTurnCompleteEvent.

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}
	c.events <- FinalTurnComplete{turn, calculateAliveCells(p, final)}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
