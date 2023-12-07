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

// RunGolRequest 是初始化broker时的请求类型，包含棋盘，线程和要处理的回合数量
type RunGolRequest struct {
	GolBoard GolBoard
	Threads  int
	Turns    int
}

// RunGolResponse 是初始化broker时的返回类型，没有实际的值被传递
type RunGolResponse struct {
}

// CurrentWorldRequest 是向broker获取当前的世界时的请求类型，没有实际的值被传递
type CurrentWorldRequest struct {
}

// CurrentWorldResponse 是向broker获取当前的世界时的返回类型，这里为了方便直接传递了棋盘
type CurrentWorldResponse struct {
	GolBoard GolBoard
}

// AliveCellsCountRequest 是向broker获取当前世界内存活细胞数量时的请求类型，没有实际的值被传递
type AliveCellsCountRequest struct {
}

// AliveCellsCountResponse 是向broker获取当前世界内存活细胞数量时的返回类型，包含当前回合数和存活细胞数量
type AliveCellsCountResponse struct {
	CurrentTurn int
	Count       int
}

// NextTurnRequest 是计算下一回合时的请求类型，这个类型同时被broker和server使用，没有实际的值被传递
type NextTurnRequest struct {
}

// NextTurnResponse 是计算下一回合时的返回类型，这个类型同时被broker和server使用，包含一个数组存储这回合有变化的细胞的坐标
type NextTurnResponse struct {
	FlippedCells []util.Cell
}

// 这些是键盘控制器用到的类型

// PauseRequest 是让broker暂停/继续处理下个回合时的请求类型，没有实际的值被传递
type PauseRequest struct {
}

// PauseResponse 是让broker暂停/继续处理下个回合时的返回类型，包含现在的回合数
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

// ServerAddress 是服务器的地址，用于光环交换，包含地址和端口
type ServerAddress struct {
	Address string
	Port    string
}

// InitRequest 是初始化server时的请求类型，包含（切割后的）棋盘，线程数量，和光环交换服务器的地址
type InitRequest struct {
	GolBoard       GolBoard
	Threads        int
	PreviousServer ServerAddress
	NextServer     ServerAddress
}

// InitResponse 是初始化server时的返回类型，没有实际的值被传递
type InitResponse struct {
}

// LineRequest 是获取光环时的请求类型，没有实际的值被传递
type LineRequest struct {
}

// LineResponse 是获取光环时的返回类型，包含一个uint8数组代表那一行光环区域的数据
type LineResponse struct {
	Line []uint8
}

// Use for autorun halo

//type WorldChangeRequest struct {
//}
//type WorldChangeResponse struct {
//	FlippedCells []util.Cell
//}
