package main

import (
	"fmt"
	"os"

	"fixture/internal/store"
)

func main() {
	// The port comes from data/port.txt.
	b, err := os.ReadFile("data/port.txt")
	if err != nil {
		fmt.Println("no port file:", err)
	}
	s := store.New()
	s.Save("greeting", "hello")
	fmt.Println(s.Load("greeting"), string(b))
}
