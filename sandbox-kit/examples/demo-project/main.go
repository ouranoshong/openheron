// Command greet prints a greeting.
//
// It is the smallest program that has a bug in it, so that a sandbox
// demonstration has something real to fix.
package main

import (
	"fmt"
	"os"
)

// Greeting returns the greeting for name.
func Greeting(name string) string {
	return "Hello, " + name
}

func main() {
	name := "world"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	fmt.Println(Greeting(name))
}
