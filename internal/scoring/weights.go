package scoring

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// Weights is a versioned weight table (DESIGN.md §7.3). Weights start hand-tuned and are later
// refitted on labeled data; the version is recorded with every detection.
type Weights struct {
	Version string
	Bias    float64
	Signals map[string]float64 // code → log-odds weight, overriding the analyzer's default
}

type weightsFile struct {
	Version *string            `yaml:"version"`
	Bias    float64            `yaml:"bias"`
	Signals map[string]float64 `yaml:"signals"`
}

//go:embed weights.yaml
var defaultWeightsYAML []byte

var defaultWeights = func() Weights {
	w, err := ParseWeights(defaultWeightsYAML)
	if err != nil {
		panic(fmt.Sprintf("embedded weights.yaml: %v", err)) // caught by the package tests
	}
	return w
}()

// DefaultWeights returns the embedded default weight table. The map is a fresh copy, so callers
// may change it.
func DefaultWeights() Weights {
	w := defaultWeights
	w.Signals = make(map[string]float64, len(defaultWeights.Signals))
	for k, v := range defaultWeights.Signals {
		w.Signals[k] = v
	}
	return w
}

// ParseWeights reads a YAML weight file:
//
//	version: "2026-10-17.1"
//	bias: -3.0
//	signals:
//	  url.blocklisted: 4.0
//
// It rejects a missing or empty version, unknown keys, empty codes and non-finite numbers, so a
// typo can't silently score every message with the wrong table.
func ParseWeights(data []byte) (Weights, error) {
	var f weightsFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return Weights{}, errors.New("weights: empty file")
		}
		return Weights{}, fmt.Errorf("weights: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Weights{}, errors.New("weights: want exactly one YAML document")
	}
	if f.Version == nil || *f.Version == "" {
		return Weights{}, errors.New("weights: version is required")
	}
	if !finite(f.Bias) {
		return Weights{}, fmt.Errorf("weights: bias %v is not a finite number", f.Bias)
	}
	w := Weights{Version: *f.Version, Bias: f.Bias, Signals: make(map[string]float64, len(f.Signals))}
	for code, v := range f.Signals {
		if code == "" {
			return Weights{}, errors.New("weights: empty signal code")
		}
		if !finite(v) {
			return Weights{}, fmt.Errorf("weights: signal %q: %v is not a finite number", code, v)
		}
		w.Signals[code] = v
	}
	return w, nil
}
