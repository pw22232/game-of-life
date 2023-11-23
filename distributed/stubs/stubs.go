package stubs

import "uk.ac.bris.cs/gameoflife/util"

type GolBoard struct {
	World       [][]uint8
	CurrentTurn int
	Width       int
	Height      int
}

// These will use by distributor

type RunGolRequest struct {
	GolBoard GolBoard
	Threads  int
	Turns    int
}
type RunGolResponse struct {
	GolBoard GolBoard
}

type CurrentWorldRequest struct {
}
type CurrentWorldResponse struct {
	GolBoard GolBoard
}

type AliveCellsCountRequest struct {
}
type AliveCellsCountResponse struct {
	CurrentTurn int
	Count       int
}

// These will use by broker

type ServerAddress struct {
	Address string
	Port    string
}

type NextTurnRequest struct {
	UpperHalo  []uint8
	GolBoard   GolBoard
	DownerHalo []uint8
	Threads    int
}
type NextTurnResponse struct {
	FlippedCells []util.Cell
}

// These will use by keyboard control

type PauseRequest struct {
}
type PauseResponse struct {
	CurrentTurn int
}

type StopRequest struct {
}
type StopResponse struct {
}

// These will use by halo switch

//type InitRequest struct {
//	GolBoard       GolBoard
//	Threads        int
//	Turns          int
//	PreviousServer ServerAddress
//	NextServer     ServerAddress
//}
//type InitResponse struct {
//}
//type FirstLineRequest struct {
//}
//type FirstLineResponse struct {
//	Line []uint8
//}
//
//type LastLineRequest struct {
//}
//type LastLineResponse struct {
//	Line []uint8
//}
//type WorldChangeRequest struct {
//}
//type WorldChangeResponse struct {
//	FlippedCells []util.Cell
//}
