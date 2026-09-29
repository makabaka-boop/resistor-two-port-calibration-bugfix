package main

import (
	"fmt"
	"math/big"
)

type calibrationResistor struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Milliohms *int64 `json:"milliohms"`
}

type calibrationMeasurement struct {
	Source               string   `json:"source"`
	Sink                 string   `json:"sink"`
	EquivalentResistance fraction `json:"equivalentResistance"`
}

type calibrationRequest struct {
	Nodes        []string                 `json:"nodes"`
	Resistors    []calibrationResistor    `json:"resistors"`
	Unknowns     []string                 `json:"unknowns"`
	Measurements []calibrationMeasurement `json:"measurements"`
}

type calibrationValue struct {
	ID        string `json:"id"`
	Milliohms int64  `json:"milliohms"`
}

type calibrationResponse struct {
	Status    string               `json:"status"`
	Solutions [][]calibrationValue `json:"solutions,omitempty"`
}

const calibrationLimit int64 = 1000000000

func parsePositiveFraction(f fraction) (*big.Rat, error) {
	num, ok := new(big.Int).SetString(f.Num, 10)
	if !ok {
		return nil, fmt.Errorf("invalid numerator")
	}
	den, ok := new(big.Int).SetString(f.Den, 10)
	if !ok || den.Sign() <= 0 || num.Sign() <= 0 {
		return nil, fmt.Errorf("equivalent resistance must be positive")
	}
	return new(big.Rat).SetFrac(num, den), nil
}

func (req *calibrationRequest) ready() (*solveRequest, [2]int, [2]*big.Rat, *apiError) {
	var indexes [2]int
	var targets [2]*big.Rat
	if len(req.Unknowns) != 2 || req.Unknowns[0] == req.Unknowns[1] || len(req.Measurements) != 2 {
		return nil, indexes, targets, &apiError{msg: "exactly two distinct unknowns and two measurements required"}
	}
	if req.Measurements[0].Source == req.Measurements[1].Source && req.Measurements[0].Sink == req.Measurements[1].Sink ||
		req.Measurements[0].Source == req.Measurements[1].Sink && req.Measurements[0].Sink == req.Measurements[1].Source {
		return nil, indexes, targets, &apiError{msg: "measurement ports must differ"}
	}
	base := &solveRequest{Nodes: req.Nodes, Resistors: make([]resistorIn, len(req.Resistors)), Source: req.Measurements[0].Source, Sink: req.Measurements[0].Sink}
	for i, r := range req.Resistors {
		value := int64(1)
		if r.Milliohms != nil {
			value = *r.Milliohms
		}
		base.Resistors[i] = resistorIn{ID: r.ID, From: r.From, To: r.To, Milliohms: value}
	}
	if err := base.validate(); err != nil {
		return nil, indexes, targets, err
	}
	seen := [2]bool{}
	for i, r := range req.Resistors {
		for k, id := range req.Unknowns {
			if r.ID == id {
				indexes[k] = i
				seen[k] = true
				if r.Milliohms != nil {
					return nil, indexes, targets, &apiError{msg: "unknown resistor must have null milliohms"}
				}
			}
		}
		if r.Milliohms == nil && r.ID != req.Unknowns[0] && r.ID != req.Unknowns[1] {
			return nil, indexes, targets, &apiError{msg: "null resistor is not declared unknown"}
		}
	}
	if !seen[0] || !seen[1] {
		return nil, indexes, targets, &apiError{msg: "unknown resistor id not found"}
	}
	for i, m := range req.Measurements {
		check := *base
		check.Source = m.Source
		check.Sink = m.Sink
		if err := check.validate(); err != nil {
			return nil, indexes, targets, err
		}
		target, err := parsePositiveFraction(m.EquivalentResistance)
		if err != nil {
			return nil, indexes, targets, &apiError{msg: fmt.Sprintf("measurement %d: %v", i, err)}
		}
		targets[i] = target
	}
	if len(components(base.Nodes, base.Resistors)) != 1 {
		return nil, indexes, targets, &apiError{msg: "calibration network must be connected"}
	}
	return base, indexes, targets, nil
}

func measuredResistance(base *solveRequest, m calibrationMeasurement) *big.Rat {
	trial := *base
	trial.Source = m.Source
	trial.Sink = m.Sink
	out := solve(&trial)
	n, _ := new(big.Int).SetString(out.EquivalentResistance.Num, 10)
	d, _ := new(big.Int).SetString(out.EquivalentResistance.Den, 10)
	return new(big.Rat).SetFrac(n, d)
}

func calibrate(req *calibrationRequest) (calibrationResponse, *apiError) {
	base, indexes, targets, err := req.ready()
	if err != nil {
		return calibrationResponse{}, err
	}
	found := [2]int64{}
	for k := 0; k < 2; k++ {
		base.Resistors[indexes[1-k]].Milliohms = 1000
		for value := int64(1); value <= 100; value++ {
			base.Resistors[indexes[k]].Milliohms = value
			if measuredResistance(base, req.Measurements[k]).Cmp(targets[k]) == 0 {
				found[k] = value
				break
			}
		}
	}
	if found[0] == 0 || found[1] == 0 {
		return calibrationResponse{Status: "NO_SOLUTION"}, nil
	}
	base.Resistors[indexes[0]].Milliohms = found[0]
	base.Resistors[indexes[1]].Milliohms = found[1]
	for k := 0; k < 2; k++ {
		if measuredResistance(base, req.Measurements[k]).Cmp(targets[k]) != 0 {
			return calibrationResponse{Status: "NO_SOLUTION"}, nil
		}
	}
	return calibrationResponse{Status: "CALIBRATED", Solutions: [][]calibrationValue{{
		{ID: req.Unknowns[0], Milliohms: found[0]},
		{ID: req.Unknowns[1], Milliohms: found[1]},
	}}}, nil
}
