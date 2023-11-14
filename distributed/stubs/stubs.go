package stubs

type GolBoard struct {
	World       [][]uint8
	CurrentTurn int
	Width       int
	Height      int
}

type NextStateRequest struct {
	GolBoard GolBoard
	Threads  int
	Turns    int
}

type NextStateResponse struct {
	GolBoard GolBoard
}
