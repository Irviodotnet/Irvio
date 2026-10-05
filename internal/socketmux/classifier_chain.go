// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import "net/netip"

// ClassifierChain stops at Control or Drop; Data advances to the next classifier.
type ClassifierChain struct {
	classifiers []Classifier
}

// NewClassifierChain ignores nil classifiers; Control/Drop terminate and unclaimed packets default to Data.
func NewClassifierChain(classifiers ...Classifier) *ClassifierChain {
	chain := &ClassifierChain{classifiers: make([]Classifier, 0, len(classifiers))}
	for _, classifier := range classifiers {
		if classifier != nil {
			chain.classifiers = append(chain.classifiers, classifier)
		}
	}
	return chain
}

// Classify implements Classifier.
func (c *ClassifierChain) Classify(payload []byte, source netip.AddrPort, family AddressFamily) PacketClass {
	if c == nil {
		return Data
	}
	for _, classifier := range c.classifiers {
		switch class := classifier.Classify(payload, source, family); class {
		case Control, Drop:
			return class
		}
	}
	return Data
}
