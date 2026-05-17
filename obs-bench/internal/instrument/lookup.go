// Package instrument — общие примитивы маршрутизации по enum.Instrument.
package instrument

import (
	"fmt"

	"obs-bench/internal/enum"
)

// Lookup возвращает значение из карты по инструменту или ошибку для неизвестного ключа.
func Lookup[T any](m map[enum.Instrument]T, i enum.Instrument) (T, error) {
	v, ok := m[i]
	if !ok {
		var z T
		return z, fmt.Errorf("unknown instrument: %q", i)
	}
	return v, nil
}
