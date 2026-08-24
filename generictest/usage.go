package generictest

import "fmt"

type Counter struct {
	N int
}

func (c *Counter) Inc() {
	c.N++
}

func BuildCounter() *Counter {
	c := &Counter{N: len(fmt.Sprint(DefaultBox.Unwrap()))}
	c.Inc()
	return c
}

func (c Counter) String() string {
	return Describe(c.N)
}
