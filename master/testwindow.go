package main

import "time"

// idleFill: at HIGH/FAST speed, keep engineering + review agents busy with DEFAULT useful work when their
// lane is empty (the owner's "use idle time, especially when speed > medium"). At medium and below they
// idle when there's no queued ticket (token-frugal). See jobs/default_work.md.
func idleFill(cfg *Config) bool {
	if isPaused(cfg) {
		return false
	}
	return paceNum(cfg) >= 4 // high or fast only
}

// iphoneTestableNow: the ONLY device-test time rule — the REAL iPhone is tested ONLY between 02:00 and
// 08:00 local (the owner connects the phone overnight and sleeps, so the fleet runs all real-iPhone tests
// then). Every other surface (sims, Android, hub, web, the device simulator) has no time restriction.
func iphoneTestableNow() bool {
	h := time.Now().Hour()
	return h >= 2 && h < 8
}
