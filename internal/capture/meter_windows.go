package capture

// Meter measures a device's level without recording it (the Audio page's
// meters). Windows shows its microphone-in-use icon while one runs on a
// microphone.
type Meter struct {
	Level
	s *Stream
}

// OpenMeter starts measuring src.
func OpenMeter(src Source) (*Meter, error) {
	m := &Meter{}
	s, err := Start(src, &m.Level, QPC(), nil)
	if err != nil {
		return nil, err
	}
	m.s = s
	return m, nil
}

// Close stops it.
func (m *Meter) Close() { m.s.Stop() }
