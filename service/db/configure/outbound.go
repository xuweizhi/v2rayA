package configure

import "fmt"

type ObservatoryType string

func (t ObservatoryType) String() string {
	return string(t)
}

const (
	LeastPing ObservatoryType = "leastping"
)

type OutboundSetting struct {
	PreProxy      string          `json:"preProxy,omitempty"`
	ProbeURL      string          `json:"probeURL"`
	ProbeInterval string          `json:"probeInterval"`
	Type          ObservatoryType `json:"type"`
}

// ValidatePreProxies checks the full graph, including currently inactive groups.
func ValidatePreProxies(settings map[string]OutboundSetting) error {
	state := map[string]int{}
	var visit func(string) error
	visit = func(group string) error {
		if state[group] == 1 {
			return fmt.Errorf("circular pre-proxy dependency at %q", group)
		}
		if state[group] == 2 {
			return nil
		}
		state[group] = 1
		if pre := settings[group].PreProxy; pre != "" {
			if _, exists := settings[pre]; !exists {
				return fmt.Errorf("pre-proxy group %q does not exist", pre)
			}
			if err := visit(pre); err != nil {
				return err
			}
		}
		state[group] = 2
		return nil
	}
	for group := range settings {
		if err := visit(group); err != nil {
			return err
		}
	}
	return nil
}

// DefaultOutboundSetting returns an OutboundSetting with default values.
func DefaultOutboundSetting() OutboundSetting {
	return OutboundSetting{
		ProbeURL:      DefaultProbeURL,
		ProbeInterval: DefaultProbeInterval,
		Type:          ObservatoryType(DefaultOutboundType),
	}
}
