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

// build 返回一个指定长度和宽度的二维矩阵
func build(height, width int) [][]uint8 {
	newMatrix := make([][]uint8, height)
	for i := range newMatrix {
		newMatrix[i] = make([]uint8, width)
	}
	return newMatrix
}

// makeImmutableWorld 将指定的矩阵转换为函数，函数只能被读取，不能被修改
func makeImmutableWorld(world [][]uint8) func(y, x int) uint8 {
	return func(y, x int) uint8 {
		return world[y][x]
	}
}

// findAliveCells 返回世界中所有存活的细胞，用于FinalTurnComplete event
func findAliveCells(p Params, immutableWorld func(y, x int) uint8) []util.Cell {
	var aliveCells []util.Cell
	for x := 0; x < p.ImageWidth; x++ {
		for y := 0; y < p.ImageHeight; y++ {
			if immutableWorld(y, x) == 255 {
				aliveCells = append(aliveCells, util.Cell{X: x, Y: y})
			}
		}
	}
	return aliveCells
}

// countAliveCells 返回世界中存活细胞的数量
func countAliveCells(p Params, immutableWorld func(y, x int) uint8) int {
	aliveCellsCount := 0
	for x := 0; x < p.ImageWidth; x++ {
		for y := 0; y < p.ImageHeight; y++ {
			if immutableWorld(y, x) == 255 {
				aliveCellsCount++
			}
		}
	}
	return aliveCellsCount
}

// calculateNextState 会计算以startY列开始，endY-1列结束的世界的下一步的状态
func calculateNextState(startY, endY, width, height int, immutableWorld func(y, x int) uint8) []util.Cell {
	// 计算所有需要改变的细胞
	var flippedCells []util.Cell
	neighboursCount := 0
	for y := startY; y < endY; y++ {
		for x := 0; x < width; x++ {
			neighboursCount = countLivingNeighbour(x, y, width, height, immutableWorld)
			if immutableWorld(y, x) == 0 && neighboursCount == 3 { // 死亡的细胞邻居刚好为3个时复活
				flippedCells = append(flippedCells, util.Cell{X: x, Y: y})
			} else if immutableWorld(y, x) == 255 && (neighboursCount < 2 || neighboursCount > 3) { // 存活的细胞邻居少于2个或多于3个时死亡
				flippedCells = append(flippedCells, util.Cell{X: x, Y: y})
			}
		}

	}
	return flippedCells
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

// isAlive 判断一个节点是否存活，支持超出边界的节点判断（上方超界则判断最后一行，左方超界则判断最后一列，以此类推）
func isAlive(x, y, width, height int, immutableWorld func(y, x int) uint8) bool {
	// if x or y equal -1, it will be the width/height - 1
	// if x or y equal width/height, it will be 0.
	x = (x + width) % width
	y = (y + height) % height
	if immutableWorld(y, x) != 0 {
		return true
	}
	return false
}

// outputPGM 将世界输出为pgm图像
func outputPGM(c distributorChannels, width, height, turn int, world [][]uint8) {
	c.ioCommand <- ioOutput
	outFilename := strconv.Itoa(width) + "x" + strconv.Itoa(height) + "x" + strconv.Itoa(turn)
	c.ioFilename <- outFilename
	// 输出世界，ioOutput通道会每次传递一个值，从世界的左上角到右下角

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c.ioOutput <- world[y][x]
		}
	}

	c.ioCommand <- ioCheckIdle
	<-c.ioIdle
	c.events <- ImageOutputComplete{CompletedTurns: turn, Filename: outFilename}
}

// worker 接收并调用calculateNextState计算世界的指定区域中哪些细胞下回合状态会发生变化。
// startY和endY用于指定计算的区域，out为返回结果的通道
func worker(startY, endY, width, height int, immutableWorld func(y, x int) uint8, out chan<- []util.Cell) {
	out <- calculateNextState(startY, endY, width, height, immutableWorld)
}

func distributor(p Params, c distributorChannels, keyPresses <-chan rune) {
	world := build(p.ImageHeight, p.ImageWidth)

	currentTurn := 0
	c.ioCommand <- ioInput
	c.ioFilename <- strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.ImageWidth)

	var processLock sync.Mutex
	// 初始化世界，ioInput管道会每次传递一个值，从世界的左上角到右下角
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			value := <-c.ioInput
			world[y][x] = value
			if value == 255 { // sdl默认是全黑色，因此在输入世界时将值为255的细胞的对应坐标的颜色翻转
				c.events <- CellFlipped{Cell: util.Cell{X: x, Y: y}}
			}
		}
	}

	// 创建Immutable对象，防止world被worker修改（防御性编程）
	immutableWorld := makeImmutableWorld(world)

	// ticker用于每两秒报告一次AliveCellsCount
	ticker := time.NewTicker(2 * time.Second)

	// quit线程在键盘按q时提示distributor停止处理回合并退出
	quit := make(chan bool)
	// isForceQuit 代表是否为按q主动退出的
	isForceQuit := false

	// 用于控制每两秒输出存活细胞数量和处理键盘输入的go线程
	go func() {
		var key rune // key 代表按下的按键，rune是int32类型的一种别名
		for {
			select {
			case <-ticker.C: // ticker的通道阻塞代表2秒到了，输出存活细胞数量
				processLock.Lock()
				c.events <- AliveCellsCount{CompletedTurns: currentTurn, CellsCount: countAliveCells(p, immutableWorld)}
				processLock.Unlock()
			case key = <-keyPresses: // keyPress通道传入按键时
				if key == 'q' { // q被按下时退出整个程序
					quit <- true
					return
				} else if key == 'p' { // p按下时暂停程序
					processLock.Lock() // 锁定互斥锁以暂停下一个回合的处理
					c.events <- StateChange{CompletedTurns: currentTurn, NewState: Paused}
					paused := true
					for paused {
						key = <-keyPresses
						if key == 'p' { // 暂停后始终等待p被按下
							ticker.Reset(2 * time.Second) // 重置ticker（防止连续触发两次）
							paused = false
							c.events <- StateChange{CompletedTurns: currentTurn, NewState: Executing}
							processLock.Unlock() // 解锁互斥锁，继续处理
						}
					}
				} else if key == 's' {
					// 按s输出当前世界，复制当前的世界到新的2D array，防止race
					currentWorld := build(p.ImageHeight, p.ImageWidth)
					processLock.Lock()
					turn := currentTurn
					for y := range world {
						for x := range world[y] {
							currentWorld[y][x] = world[y][x] // 复制每个位置上细胞的值
						}
					}
					processLock.Unlock()
					go outputPGM(c, p.ImageWidth, p.ImageHeight, turn, currentWorld) // 由于outputPGM的通道等待时间较长，放进go线程里处理
				}
			}
		}
	}()

	// 根据需要处理的回合数量进行循环，当用户按下退出时直接结束循环
	for currentTurn = 0; currentTurn < p.Turns && !isForceQuit; {
		var outChannels []chan []util.Cell         // outChannels 存储所有worker会用到的通道
		averageHeight := p.ImageHeight / p.Threads // 计算每个worker要处理的平均行数
		restHeight := p.ImageHeight % p.Threads    // 计算余数
		currentHeight := 0                         // currentHeight 代表worker要从哪行开始处理
		for i := 0; i < p.Threads; i++ {
			size := averageHeight // size 代表当前worker要处理的行数
			// 根据余数给前几个threads多分配一行的工作
			if i < restHeight {
				size += 1
			}
			outChannel := make(chan []util.Cell)          // 创建一个新的通道给worker
			outChannels = append(outChannels, outChannel) // 保存到列表中之后读取
			go worker(currentHeight, currentHeight+size, p.ImageWidth, p.ImageHeight, immutableWorld, outChannel)
			currentHeight += size
		}

		var flippedCells []util.Cell     // flippedCells 存储了所有状态改变了的细胞的坐标
		for i := 0; i < p.Threads; i++ { // 通过循环从每个worker的通道获取数据
			flippedCells = append(flippedCells, <-outChannels[i]...) // 将每个通道返回的细胞列表追加到flippedCells
		}

		// 将所有细胞状态改变应用到世界上
		processLock.Lock() // 因为这里要修改世界和回合了，所以锁定互斥锁
		for _, flippedCell := range flippedCells {
			if world[flippedCell.Y][flippedCell.X] == 255 { // 存活的细胞状态变为死亡
				world[flippedCell.Y][flippedCell.X] = 0
			} else {
				world[flippedCell.Y][flippedCell.X] = 255 // 死亡的细胞状态变为存活
			}
			c.events <- CellFlipped{currentTurn, flippedCell} // 通知sdl有一个细胞状态发生了改变
		}
		currentTurn++                                         // currentTurn 会被其他函数调用，因此修改时也要锁定互斥锁
		c.events <- TurnComplete{CompletedTurns: currentTurn} // 通知sdl回合已经完成
		processLock.Unlock()
		select { // 检查是否按q退出了
		case <-quit:
			isForceQuit = true // quit通道有东西则代表按q退出了，将isForceQuit设置为true
		default:
			break
		}
	}

	ticker.Stop()
	outputPGM(c, p.ImageWidth, p.ImageHeight, currentTurn, world) // 不论是否是按q退出都要输出一个pgm图像，按q退出时这里输出最后一回合
	if !isForceQuit {                                             // 如果不是按q退出，就代表要求的回合都执行完了，向events通道发送FinalTurnComplete
		c.events <- FinalTurnComplete{currentTurn, findAliveCells(p, immutableWorld)}
	}

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle

	c.events <- StateChange{currentTurn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
