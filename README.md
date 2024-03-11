# Simulation of Conway's Game Of Life 
## Parallel and Distributed programming
Golang implementation of Conway's Game of Life. Also known simply as Life, is a cellular automaton devised by the British mathematician John Horton Conway in 1970.

The "game" is a zero-player game, meaning that its evolution is determined by its initial state, requiring no further input. One interacts with the Game of Life by creating an initial configuration and observing how it evolves, or, for advanced "players", by creating patterns with particular properties.

1.Any live cell with fewer than two live neighbours dies, as if caused by under-population.

2.Any live cell with two or three live neighbours lives on to the next generation.

3.Any live cell with more than three live neighbours dies, as if by over-population.

4.Any dead cell with exactly three live neighbours becomes a live cell, as if by reproduction.

### Part1. Parallel

We Implement the logic to visualise the state of the game using SDL.  
Also, the following are control rules. Note that the goroutine running SDL provides us with a channel containing the relevant keypresses.

If s is pressed, generate a PGM file with the current state of the board.
If q is pressed, generate a PGM file with the current state of the board and then terminate the program. Your program should not continue to execute all turns set in gol.Params.Turns.
If p is pressed, pause the processing and print the current turn that is being processed. If p is pressed again resume the processing and print "Continuing". It is not necessary for q and s to work while the execution is paused.

Test the visualisation and control rules by running go run .

