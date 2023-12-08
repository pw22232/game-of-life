package gol

import (
	"fmt"
	"net/rpc"
	"os"
	"strconv"
	"time"
	"uk.ac.bris.cs/gameoflife/stubs"
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

// dialError 处理发生的错误，这里发现错误就直接退出程序
func dialError(err error, c distributorChannels) {
	if err != nil {
		fmt.Println(err) // 输出错误
		c.ioCommand <- ioCheckIdle
		<-c.ioIdle
		c.events <- StateChange{NewState: Quitting}
		close(c.events) // 这四步用于在退出前等待对文件处理的程序完成操作
		os.Exit(1)
	}
}

// build 接收长度和宽度并生成一个指定长度x宽度的2D矩阵
func build(height, width int) [][]uint8 {
	newMatrix := make([][]uint8, height)
	for i := range newMatrix {
		newMatrix[i] = make([]uint8, width)
	}
	return newMatrix
}

// findAliveCells 返回世界中所有存活的细胞的坐标（保存为util.Cell类型的数组），用于FinalTurnComplete event
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

// outputPGM 将世界输出为pgm图像
func outputPGM(c distributorChannels, p Params, turn int, world [][]uint8) {
	c.ioCommand <- ioOutput // 设置io为输出模式
	// 文件名：图像宽度x图像长度x当前回合数
	outFilename := strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.ImageWidth) + "x" + strconv.Itoa(turn)
	c.ioFilename <- outFilename
	// 输出世界，ioOutput通道会每次传递一个细胞的值，从世界的左上角到右下角
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			c.ioOutput <- world[y][x]
		}
	}
	// 检查是否已经输出完毕
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle
	// 输出完毕后，向events通道传递ImageOutputComplete事件
	c.events <- ImageOutputComplete{CompletedTurns: turn, Filename: outFilename}
}

// distributor 连接到broker并传递当前的世界，之后broker完全自动的处理所有回合
func distributor(p Params, c distributorChannels, keyPresses <-chan rune) {
	address := "localhost"
	port := "8080"
	// broker运行在本地，用localhost作为地址连接
	broker, err := rpc.Dial("tcp", address+":"+port)
	dialError(err, c)
	turn := 0
	// 将io切换为输入（读图）模式
	c.ioCommand <- ioInput
	c.ioFilename <- strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(p.ImageWidth)

	// 初始化世界，ioInput管道会每次传递一个值，从世界的左上角到右下角
	world := build(p.ImageHeight, p.ImageWidth)
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			value := <-c.ioInput
			world[y][x] = value
		}
	}
	// 如果要执行的回合数量大于0
	if p.Turns > 0 {
		// finalTurnFinish 通道用于在正常处理完所有回合后收集棋盘的状态，通道内为一整个棋盘
		finalTurnFinish := make(chan stubs.GolBoard)
		// countFinish 通道用于在每两秒输出一次存活细胞数量
		countFinish := make(chan stubs.AliveCellsCountResponse)
		quit := make(chan bool)

		golBoard := stubs.GolBoard{World: world, CurrentTurn: 0, Width: p.ImageWidth, Height: p.ImageHeight}
		req := stubs.RunGolRequest{GolBoard: golBoard, Turns: p.Turns, Threads: p.Threads}
		var countReq stubs.AliveCellsCountRequest
		var res stubs.RunGolResponse
		var aliveRes stubs.AliveCellsCountResponse
		var worldReq stubs.CurrentWorldRequest
		ticker := time.NewTicker(2 * time.Second)
		// 调用服务器运行所有的回合
		go func() {
			runErr := broker.Call("Broker.RunGol", req, &res)
			if runErr == nil {
				finalTurnFinish <- res.GolBoard
			}
		}()

		go func() {
			var key rune
			for {
				select {
				case key = <-keyPresses:
					if key == 'q' {
						var countRes stubs.AliveCellsCountResponse
						countErr := broker.Call("Broker.CountAliveCells", countReq, &countRes)
						dialError(countErr, c)
						turn = countRes.CurrentTurn
						quit <- true
					} else if key == 'k' {
						var worldRes stubs.CurrentWorldResponse
						worldErr := broker.Call("Broker.GetWorld", worldReq, &worldRes)
						dialError(worldErr, c)
						outputPGM(c, p, worldRes.CurrentTurn, worldRes.World)
						turn = worldRes.CurrentTurn
						_ = broker.Call("Broker.Stop", stubs.StopRequest{}, stubs.StopResponse{})
						quit <- true
					} else if key == 'p' {
						pauseRes := stubs.PauseResponse{}
						pauseErr := broker.Call("Broker.Pause", stubs.PauseRequest{}, &pauseRes)
						dialError(pauseErr, c)
						c.events <- StateChange{CompletedTurns: pauseRes.CurrentTurn, NewState: Paused}
						paused := true
						for paused {
							key = <-keyPresses
							if key == 'p' {
								res := stubs.PauseResponse{}
								pauseErr = broker.Call("Broker.Pause", stubs.PauseRequest{}, &res)
								dialError(pauseErr, c)
								ticker.Reset(2 * time.Second) // 重新开始ticker计时
								paused = false
								c.events <- StateChange{CompletedTurns: res.CurrentTurn, NewState: Executing}
							}
						}
					} else if key == 's' {
						var worldRes stubs.CurrentWorldResponse
						worldErr := broker.Call("Broker.GetWorld", worldReq, &worldRes)
						dialError(worldErr, c)
						go outputPGM(c, p, worldRes.CurrentTurn, worldRes.World)
					}
				case <-ticker.C:
					var countRes stubs.AliveCellsCountResponse
					_ = broker.Call("Broker.CountAliveCells", countReq, &countRes)
					countFinish <- countRes
				}
			}
		}()

		finishFlag := false
		for {
			select {
			case board := <-finalTurnFinish:
				ticker.Stop()
				finishFlag = true
				turn = board.CurrentTurn
				outputPGM(c, p, board.CurrentTurn, board.World)
				c.events <- FinalTurnComplete{board.CurrentTurn, findAliveCells(p, board.World)}
			case <-quit:
				finishFlag = true
			case aliveRes = <-countFinish:
				c.events <- AliveCellsCount{CompletedTurns: aliveRes.CurrentTurn, CellsCount: aliveRes.Count}
			}
			if finishFlag {
				break
			}
		}
	} else {
		board := stubs.GolBoard{World: world, CurrentTurn: 0, Width: p.ImageWidth, Height: p.ImageHeight}
		outputPGM(c, p, board.CurrentTurn, board.World)
		c.events <- FinalTurnComplete{board.CurrentTurn, findAliveCells(p, board.World)}
	}
	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
