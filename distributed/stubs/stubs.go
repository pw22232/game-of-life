package stubs

type GolBoard struct {
	World       [][]uint8
	CurrentTurn int
	Width       int
	Height      int
}

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

type PauseRequest struct {
}

type PauseResponse struct {
	CurrentTurn int
}

type StopRequest struct {
}

type StopResponse struct {
}
