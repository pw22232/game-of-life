package stubs

import "uk.ac.bris.cs/gameoflife/gol"

type golBoard struct {
	World  [][]uint8
	Turn   int
	Width  int
	Height int
}

type nextStateRequest struct {
	GolBoard golBoard
	p        gol.Params
}

type nextStateResponse struct {
	golBoard golBoard
}
