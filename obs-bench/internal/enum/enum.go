package enum

import (
	"fmt"
	"strings"
)

type Instrument string

const (
	InstrumentPrometheus      Instrument = "prometheus"
	InstrumentVictoriaMetrics Instrument = "victoria_metrics"
	InstrumentLoki            Instrument = "loki"
	InstrumentOpenSearch      Instrument = "opensearch"
)

// AllInstruments — единый перечень поддерживаемых инструментов.
var AllInstruments = []Instrument{
	InstrumentPrometheus,
	InstrumentVictoriaMetrics,
	InstrumentLoki,
	InstrumentOpenSearch,
}

// IsLogBackend — логи (Loki / OpenSearch): log-load-generator + query API.
func IsLogBackend(i Instrument) bool {
	switch i {
	case InstrumentLoki, InstrumentOpenSearch:
		return true
	default:
		return false
	}
}

// ParseInstrument разбирает флаг/аргумент CLI по строковому значению инструмента.
func ParseInstrument(s string) (Instrument, error) {
	s = strings.TrimSpace(s)
	for _, i := range AllInstruments {
		if string(i) == s {
			return i, nil
		}
	}
	return "", fmt.Errorf("instrument must be one of: %s", InstrumentFlagChoices())
}

// InstrumentFlagChoices — фрагмент для подсказок в cobra (например "prometheus" or "victoria_metrics").
func InstrumentFlagChoices() string {
	parts := make([]string, len(AllInstruments))
	for i, inst := range AllInstruments {
		parts[i] = fmt.Sprintf("%q", inst)
	}
	return strings.Join(parts, " or ")
}

// EnsureAllInstrumentsInMap проверяет, что для каждого элемента AllInstruments в карте есть ключ
// (реестры Fx, топология и т.п. не забыли новый инструмент).
func EnsureAllInstrumentsInMap[V any](m map[Instrument]V) error {
	for _, i := range AllInstruments {
		if _, ok := m[i]; !ok {
			return fmt.Errorf("instrument registry missing %q (sync with enum.AllInstruments)", i)
		}
	}
	return nil
}
