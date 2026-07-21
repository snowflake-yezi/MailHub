package filtercontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Score stores thousandths to keep checksums independent of floating-point
// formatting. JSON uses the shortest decimal form with at most three places.
type Score int64

func ParseScore(input string) (Score, error) {
	if input == "" || strings.TrimSpace(input) != input {
		return 0, errors.New("score must be a non-empty canonical decimal")
	}
	negative := false
	if input[0] == '-' {
		negative = true
		input = input[1:]
	} else if input[0] == '+' {
		return 0, errors.New("score must not use a leading plus sign")
	}
	parts := strings.Split(input, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("score must be a decimal number")
	}
	if len(parts[0]) > 1 && parts[0][0] == '0' {
		return 0, errors.New("score must not contain leading zeroes")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 32)
	if err != nil {
		return 0, errors.New("score integer part is invalid")
	}
	fraction := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > 3 {
			return 0, errors.New("score must have one to three decimal places")
		}
		for _, r := range parts[1] {
			if r < '0' || r > '9' {
				return 0, errors.New("score fraction is invalid")
			}
		}
		padded := parts[1] + strings.Repeat("0", 3-len(parts[1]))
		fraction, _ = strconv.ParseInt(padded, 10, 16)
	}
	value := Score(whole*1000 + fraction)
	if negative {
		value = -value
	}
	return value, nil
}

func (s Score) String() string {
	value := int64(s)
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	whole := value / 1000
	fraction := value % 1000
	if fraction == 0 {
		return sign + strconv.FormatInt(whole, 10)
	}
	fractionText := strings.TrimRight(fmt.Sprintf("%03d", fraction), "0")
	return sign + strconv.FormatInt(whole, 10) + "." + fractionText
}

func (s Score) MarshalJSON() ([]byte, error) { return []byte(s.String()), nil }

func (s *Score) UnmarshalJSON(data []byte) error {
	if s == nil {
		return errors.New("cannot unmarshal score into nil receiver")
	}
	if bytes.Equal(data, []byte("null")) {
		return errors.New("score cannot be null")
	}
	parsed, err := ParseScore(string(data))
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

func (v ConditionValue) MarshalJSON() ([]byte, error) {
	switch v.kind {
	case conditionNull:
		return []byte("null"), nil
	case conditionString:
		return json.Marshal(v.text)
	case conditionBool:
		return json.Marshal(v.boolean)
	case conditionInteger:
		return []byte(strconv.FormatInt(v.integer, 10)), nil
	default:
		return nil, errors.New("invalid condition value kind")
	}
}

func (v *ConditionValue) UnmarshalJSON(data []byte) error {
	if v == nil {
		return errors.New("cannot unmarshal condition value into nil receiver")
	}
	*v = ConditionValue{}
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	if len(data) == 0 {
		return errors.New("empty condition value")
	}
	switch data[0] {
	case '"':
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*v = StringValue(value)
		return nil
	case 't', 'f':
		var value bool
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*v = BoolValue(value)
		return nil
	default:
		if bytes.ContainsAny(data, ".eE") {
			return errors.New("condition number must be an integer")
		}
		value, err := strconv.ParseInt(string(data), 10, 64)
		if err != nil {
			return errors.New("condition value must be a string, boolean, integer, or null")
		}
		*v = IntegerValue(value)
		return nil
	}
}
