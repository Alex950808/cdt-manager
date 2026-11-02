package alicloud

import (
	"encoding/json"
	"strconv"
	"strings"
)

const httpPost = "POST"

// flexFloat 兼容阿里云返回金额的两种形态："12.34"（字符串）与 12.34（数字），且支持千分位逗号。
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" {
		*f = 0
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		s = str
	}
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if s == "" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*f = flexFloat(v)
	return nil
}
