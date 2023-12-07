package stubs

import "uk.ac.bris.cs/gameoflife/util"

// GolBoard 代表棋盘，包含世界（2D数组），当前回合和世界长宽
type GolBoard struct {
	World       [][]uint8
	CurrentTurn int
	Width       int
	Height      int
}

// 这些是和broker通信时要用到的类型

// RunGolRequest 是调用broker开始处理时的请求类型，包含棋盘，线程和要处理的回合数量
type RunGolRequest struct {
	GolBoard GolBoard
	Threads  int
	Turns    int
}

// RunGolResponse 是broker处理完成时的返回类型，包含最终回合的棋盘
type RunGolResponse struct {
	GolBoard GolBoard
}

// CurrentWorldRequest 是向broker获取当前的世界时的请求类型，没有实际的值被传递
type CurrentWorldRequest struct {
}

// CurrentWorldResponse 是向broker获取当前的世界时的返回类型，包含当前的世界和当前回合数
type CurrentWorldResponse struct {
	World       [][]uint8
	CurrentTurn int
}

// AliveCellsCountRequest 是向broker获取当前世界内存活细胞数量时的请求类型，没有实际的值被传递
type AliveCellsCountRequest struct {
}

// AliveCellsCountResponse 是向broker获取当前世界内存活细胞数量时的返回类型，包含当前回合数和存活细胞数量
type AliveCellsCountResponse struct {
	CurrentTurn int
	Count       int
}

// 这些是和server通信时要用到的类型

// ServerAddress 是服务器的地址，用于光环交换，包含地址和端口
type ServerAddress struct {
	Address string
	Port    string
}

// InitServerRequest 是初始化server时的请求类型，包含（切割后的）棋盘，线程数量，总回合数，世界Y轴开始的位置对应的行数，和光环交换服务器的地址
type InitServerRequest struct {
	GolBoard       GolBoard
	Threads        int
	Turns          int
	StartY         int
	PreviousServer ServerAddress
	NextServer     ServerAddress
}

// InitServerResponse 是初始化server时的返回类型，没有实际的值被传递
type InitServerResponse struct {
}

// RunServerRequest 是让服务器开始自动运行时的请求类型，没有实际的值被传递
type RunServerRequest struct {
}

// RunServerResponse 是让服务器运行完成后的返回类型，包含最终回合的世界切片
type RunServerResponse struct {
	World [][]uint8
}

// 这两个类型原本是为了光环交换的功能预留的，后来不需要使用了，忘记删掉了

type ReadyToReadRequest struct {
}
type ReadyToReadResponse struct {
}

// WorldChangeRequest 是获取服务器当前回合与最世界开始之间的细胞变化时的请求类型，没有实际的值被传递
type WorldChangeRequest struct {
}

// WorldChangeResponse 是获取服务器当前回合与最世界开始之间的细胞变化时的返回类型
// 包含一个字典保存了所有有变化的细胞，一个数组包含了上一回合做出的改变（自动光环服务器之间有1回合差距）
// 其实这里应该让服务器先把字典转换成数组再返回会更好，因为map比数组占用多一倍内存
type WorldChangeResponse struct {
	FlippedCellsMap    map[util.Cell]bool
	FlippedCellsBuffer []util.Cell
	CurrentTurn        int
}

// 这些是键盘控制器用到的类型

// PauseRequest 是让broker和server暂停/继续处理下个回合时的请求类型，没有实际的值被传递
type PauseRequest struct {
}

// PauseResponse 是让broker和server暂停/继续处理下个回合时的返回类型，包含现在的回合数
type PauseResponse struct {
	CurrentTurn int
}

// StopRequest 是让broker和server关闭时的请求类型，没有实际的值被传递
type StopRequest struct {
}

// StopResponse 是让broker和server关闭时的返回类型，实际上没有任何作用，因为直接退出没有返回
type StopResponse struct {
}

// 光环交换时用到的类型

// LineRequest 是获取光环时的请求类型，没有实际的值被传递
type LineRequest struct {
}

// LineResponse 是获取光环时的返回类型，包含一个uint8数组代表那一行光环区域的数据
type LineResponse struct {
	Line []uint8
}
