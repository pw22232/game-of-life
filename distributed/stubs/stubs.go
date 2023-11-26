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
	World       [][]uint8
	CurrentTurn int
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

type InitServerRequest struct {
	GolBoard       GolBoard
	Threads        int
	Turns          int
	PreviousServer ServerAddress
	NextServer     ServerAddress
}
type InitServerResponse struct {
}

type RunServerRequest struct {
}
type RunServerResponse struct {
	World [][]uint8
}

type ReadyToReadRequest struct {
}
type ReadyToReadResponse struct {
}

type WorldChangeRequest struct {
}
type WorldChangeResponse struct {
	FlippedCells []util.Cell
	CurrentTurn  int
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

type LineRequest struct {
}
type LineResponse struct {
	Line []uint8
}
