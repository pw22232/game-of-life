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

// build 返回一个指定长度和宽度的二维矩阵
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

// distributor 连接到broker并传递当前的世界，之后每个回合都会调用broker的方法计算下回合的世界
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
			if value == 255 {
				c.events <- CellFlipped{Cell: util.Cell{X: x, Y: y}}
			}
		}
	}
	// 如果有要执行的回合
	if p.Turns > 0 {
		// finalTurnFinish 通道用于在正常处理完所有回合后收集棋盘的状态，通道内为一整个棋盘
		finalTurnFinish := make(chan stubs.GolBoard)
		// countFinish 通道用于在每两秒输出一次存活细胞数量
		// 注意：这里应该创建一个countRes类型的通道，目前的方法有可能导致race。
		// 具体原因在ticker代码部分有说，但由于ticker每2秒才执行一次，理论上不会race。
		countFinish := make(chan bool)
		// quit 通道内有值时代表用户按q或k强制退出了
		quit := make(chan bool)
		// end 通道用于提醒回合执行器停止执行回合
		end := make(chan bool)

		// 创建一个棋盘类型，用于传给broker
		golBoard := stubs.GolBoard{World: world, Width: p.ImageWidth, Height: p.ImageHeight}

		// 初始化一些request供其他线程使用，其实这里不应该初始化任何response，因为有可能导致race
		// 但这里还留着的response都是我检查过不会race了所以留着以供其他线程使用的。
		req := stubs.RunGolRequest{GolBoard: golBoard, Turns: p.Turns, Threads: p.Threads}
		var countReq stubs.AliveCellsCountRequest
		var res stubs.RunGolResponse
		var countRes stubs.AliveCellsCountResponse
		var worldReq stubs.CurrentWorldRequest

		// 创建一个的ticker，每两秒会向ticker.C通道传值
		ticker := time.NewTicker(2 * time.Second)

		// 调用broker初始化（RunGol应该改名叫init，忘记改了）
		initErr := broker.Call("Broker.RunGol", req, &res)
		dialError(initErr, c)

		// 回合控制器线程，用于每个回合调用broker计算结果再获取
		// 其实这里的代码放到底下那个线程作为select的default case会更好
		go func() {
			endFlag := false // endFlag 为true时停止这个线程
			for turn = 0; turn < p.Turns; turn++ {
				// 每个回合都调用broker的NextTurn计算世界状态
				nextRes := stubs.NextTurnResponse{}
				nextErr := broker.Call("Broker.NextTurn", stubs.NextTurnRequest{}, &nextRes)
				dialError(nextErr, c)
				// response内包含这回合发生改变的细胞的位置
				for _, flippedCell := range nextRes.FlippedCells {
					// 和sdl同步要改变的细胞的位置
					c.events <- CellFlipped{turn, flippedCell}
				}
				// 回合结束发送TurnComplete事件，其实这里应该turn+1，但是不影响测试
				c.events <- TurnComplete{CompletedTurns: turn}
				select {
				case <-end:
					endFlag = true // 如果用户按下q或k则设置endFlag为true
				default:
					break
				}
				if endFlag {
					return
				}
			}
			// 如果所有回合都执行完毕，获取最终的棋盘
			var endRes stubs.CurrentWorldResponse
			worldErr := broker.Call("Broker.GetWorld", worldReq, &endRes)
			dialError(worldErr, c)
			// 将最终棋盘传进finalTurnFinish通道，代表处理完成
			finalTurnFinish <- endRes.GolBoard
		}()

		// 用于控制每两秒输出存活细胞数量和处理键盘输入的go线程
		// 使用func直接创建内嵌函数可以免去传递参数的麻烦，但是要特别注意在写入变量时是否会引发race
		go func() {
			// 按下的按键会被存储为rune类型
			var key rune
			var endFlag = false // endFlag 为true时停止这个线程
			for !endFlag {
				select {
				case key = <-keyPresses: // 情况1：用户按下了按键
					if key == 'q' { // 当用户按下q键时，退出本程序但不退出broker和server
						// 这里是想调用Broker的CountAliveCells获取当前在执行的回合数来在退出时显示
						// 但是因为我们每回合和broker同步所以这行代码其实没有用处，应该删掉
						_ = broker.Call("Broker.CountAliveCells", countReq, &countRes)
						// 终止回合控制器，通知主程序关闭，退出本线程
						end <- true
						quit <- true
						endFlag = true
					} else if key == 'k' { // 当用户按下k键时，退出本程序、broker和server
						// 退出之前调用broker获取当前的世界并输出图像
						var worldRes stubs.CurrentWorldResponse
						worldErr := broker.Call("Broker.GetWorld", worldReq, &worldRes)
						dialError(worldErr, c)
						outputPGM(c, p, worldRes.GolBoard.CurrentTurn, worldRes.GolBoard.World)
						// 调用broker的stop方法，broker会在通知服务器关闭后停止
						_ = broker.Call("Broker.Stop", stubs.StopRequest{}, stubs.StopResponse{})
						// 终止回合控制器，通知主程序关闭，退出本线程
						end <- true
						quit <- true
						endFlag = true
					} else if key == 'p' { // 当用户按下p键时，暂停处理下回合和其他事件
						// 暂停时调用broker的pause方法来暂停broker处理下回合，同时broker会返回在哪个回合被暂停了
						pauseRes := stubs.PauseResponse{}
						pauseErr := broker.Call("Broker.Pause", stubs.PauseRequest{}, &pauseRes)
						dialError(pauseErr, c)
						// 向event通道发送StateChange事件表示已暂停
						c.events <- StateChange{CompletedTurns: pauseRes.CurrentTurn, NewState: Paused}
						paused := true // 用于下面的for循环
						for paused {
							key = <-keyPresses
							if key == 'p' { // 当用户再次按下p键时，继续运行
								// 再次调用broker的pause方法会让broker继续处理下回合
								// 这里的名字忘改了，应该是pauseRes
								res := stubs.PauseResponse{}
								pauseErr = broker.Call("Broker.Pause", stubs.PauseRequest{}, &res)
								dialError(pauseErr, c)
								ticker.Reset(2 * time.Second) // 重置ticker来防止连续触发两次
								paused = false                // 结束for循环
								// 向event通道发送StateChange事件表示继续运行
								c.events <- StateChange{CompletedTurns: res.CurrentTurn, NewState: Executing}
							}
						}
					} else if key == 's' { // 当用户按下s键时，输出当前回合的世界的图像
						// 调用broker返回当前世界
						var worldRes stubs.CurrentWorldResponse
						worldErr := broker.Call("Broker.GetWorld", worldReq, &worldRes)
						dialError(worldErr, c)
						// 在子线程内输出图像，防止卡顿，由于worldRes在上面每次重新创建，因此不会发生race
						go outputPGM(c, p, worldRes.GolBoard.CurrentTurn, worldRes.GolBoard.World)
					}
				case <-ticker.C: // 情况2，ticker到两秒了
					// 调用Broker返回当前回合和世界内存活的细胞数量
					aliveErr := broker.Call("Broker.CountAliveCells", countReq, &countRes)
					dialError(aliveErr, c)
					// 此处直接向通道传true，主线程会直接调用countRes内的值
					// 但如果主线程处理过程中ticker又触发了countRes就会更改（虽然这不可能发生）
					// 比较好的处理方式应该是在这里直接创建countRes，然后将countRes的值放进通道内给主线程读取
					countFinish <- true
				}
			}
		}()

		finishFlag := false // finishFlag为true时退出主程序
		for {
			select {
			case board := <-finalTurnFinish: // 如果finalTurnFinish通道传入，则代表正常执行完了所有回合
				finishFlag = true
				turn = board.CurrentTurn                        // 这句话其实没有用处，应该改为停止ticker以解决下面的一个问题
				outputPGM(c, p, board.CurrentTurn, board.World) // 最终回合完成后输出图像
				c.events <- FinalTurnComplete{board.CurrentTurn, findAliveCells(p, board.World)}
			case <-quit:
				// 这里应该停止ticker以解决下面的一个问题
				finishFlag = true
			case <-countFinish:
				// 这里在获取存活细胞数量完成后向event传输值，原本应该放在上面的子线程里
				// 至于放在这里的原因是我忘记在处理完成或用户输入退出后停止ticker，放在这里避免ticker继续工作
				c.events <- AliveCellsCount{CompletedTurns: countRes.CurrentTurn, CellsCount: countRes.Count}
			}
			if finishFlag {
				break
			}
		}
	} else { // 如果回合数为0则不需要连接broker处理数据，直接输出图像退出即可
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
