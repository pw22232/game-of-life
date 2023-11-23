package stubs

import "uk.ac.bris.cs/gameoflife/util"

type GolBoard struct {
	World       [][]uint8
	CurrentTurn int
	Width       int
	Height      int
}

type ServerIndex int

const (
	First ServerIndex = iota
	Middle
	Last
)

type ServerAddress struct {
	Address string
	Port    string
}

type RunGolRequest struct {
	GolBoard GolBoard
	Threads  int
	Turns    int
}
type RunGolResponse struct {
	GolBoard GolBoard
}

type InitRequest struct {
	GolBoard       GolBoard
	Threads        int
	Turns          int
	ServerIndex    ServerIndex
	PreviousServer ServerAddress
	NextServer     ServerAddress
}
type InitResponse struct {
}

type NextTurnRequest struct {
}
type NextTurnResponse struct {
}

type FirstLineRequest struct {
}
type FirstLineResponse struct {
	Line []uint8
}

type LastLineRequest struct {
}
type LastLineResponse struct {
	Line []uint8
}

type CurrentWorldRequest struct {
}
type CurrentWorldResponse struct {
	GolBoard GolBoard
}

type WorldChangeRequest struct {
}
type WorldChangeResponse struct {
	FlippedCells []util.Cell
}

type AliveCellsCountRequest struct {
}
type AliveCellsCountResponse struct {
	CurrentTurn int
	Count       int
}

type PauseRequest struct {
}
type PauseResponse struct {
	CurrentTurn int
}

type StopRequest struct {
}
type StopResponse struct {
}
