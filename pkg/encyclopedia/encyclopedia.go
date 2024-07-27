package encyclopedia

type Encyclopedia interface {
	Aircraft() map[string]Aircraft
}

type encyclopedia struct {
}

var _ Encyclopedia = &encyclopedia{}

func New() Encyclopedia {
	return &encyclopedia{}
}

func (e *encyclopedia) Aircraft() map[string]Aircraft {
	var out = make(map[string]Aircraft)
	for _, a := range aircraftData {
		out[a.ACMIShortName] = a
	}
	return out
}
