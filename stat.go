package main

type ServerStat struct {
	rttMax int

	avgRTT           int
	consecutiveFails int
	recentFails      int
	recent           [8]bool // latest 8 samples
	recentIdx        int
	recentCount      int
}

func newServerStat(rttMax int) *ServerStat {
	return &ServerStat{
		rttMax: rttMax,
	}
}

func (s *ServerStat) Put(rtt int, fail bool) {
	if rtt <= 0 {
		rtt = s.rttMax
	}
	if rtt > s.rttMax {
		rtt = s.rttMax
	}

	if s.recentCount == len(s.recent) {
		if s.recent[s.recentIdx] {
			s.recentFails--
		}
	} else {
		s.recentCount++
	}
	s.recent[s.recentIdx] = fail
	if fail {
		s.recentFails++
	}
	s.recentIdx = (s.recentIdx + 1) % len(s.recent)

	if fail {
		s.consecutiveFails++
		return
	}

	if s.avgRTT == 0 {
		s.avgRTT = rtt
	} else {
		s.avgRTT = (s.avgRTT*7 + rtt) / 8
	}
	s.consecutiveFails = 0
}

// Score returns a score for the server, where lower is better.
func (s *ServerStat) Score() int {
	// smooths successful RTTs instead of reacting to a single sample
	score := s.avgRTT
	if score == 0 {
		score = s.rttMax
	}

	// Keep isolated probe loss cheap, but let repeated loss slowly drag
	// the score upward before hard failover logic kicks in.
	score += s.recentFails * max(1, s.rttMax/128)

	// penalizes consecutive failures hard, so a fully dead server loses quickly
	switch {
	case s.consecutiveFails >= 2:
		score += (s.consecutiveFails - 1) * s.rttMax * 2
	case s.consecutiveFails == 1 && s.avgRTT == 0:
		score += s.rttMax
	}
	return score
}
