package generictest

import "fmt"

const PackageName = "generictest"

var DefaultBox = NewBox(1)

type Box[T any] struct {
	Value T
}

func NewBox[T any](v T) Box[T] {
	return Box[T]{Value: v}
}

func (b Box[T]) Unwrap() T {
	return b.Value
}

type Pair[A, B any] struct {
	First  A
	Second B
}

func MakePair[A, B any](a A, b B) Pair[A, B] {
	return Pair[A, B]{First: a, Second: b}
}

type Number interface {
	~int | ~int64
}

func Sum[T Number](values []T) T {
	var total T
	for _, v := range values {
		total += v
	}
	return total
}

func Describe[T any](v T) string {
	return fmt.Sprintf("%s:%v", PackageName, v)
}
