package atoms

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func utilityDefinitions() []Definition {
	return []Definition{
		{Name: "current_time", Description: "Read the actual server clock in an optional IANA timezone (default Asia/Shanghai).", Parameters: json.RawMessage(`{"type":"object","properties":{"timezone":{"type":"string","minLength":1,"maxLength":128}},"additionalProperties":false}`), Run: func(ctx context.Context, _ Invocation, args string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var a struct {
				Timezone string `json:"timezone"`
			}
			if json.Unmarshal([]byte(args), &a) != nil {
				return "", domain.ErrInvalid
			}
			if a.Timezone == "" {
				a.Timezone = "Asia/Shanghai"
			}
			loc, err := time.LoadLocation(a.Timezone)
			if err != nil {
				return "", domain.ErrInvalid
			}
			return encodeCommunity(map[string]any{"time": time.Now().In(loc).Format(time.RFC3339Nano), "timezone": a.Timezone})
		}},
		{Name: "calculate", Description: "Evaluate bounded arithmetic (+ - * / %, parentheses, integer powers ^). Returns an exact rational plus a decimal rounded to 18 places; no code execution.", Parameters: json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string","minLength":1,"maxLength":4096}},"required":["expression"],"additionalProperties":false}`), Run: func(ctx context.Context, _ Invocation, args string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var a struct {
				Expression string `json:"expression"`
			}
			if json.Unmarshal([]byte(args), &a) != nil {
				return "", domain.ErrInvalid
			}
			p := arithmetic{input: a.Expression}
			if len(p.input) == 0 || len(p.input) > 4096 {
				return "", domain.ErrInvalid
			}
			v, err := p.expression(0, 0)
			p.space()
			if err != nil || p.pos != len(p.input) {
				return "", domain.ErrInvalid
			}
			decimal := strings.TrimRight(strings.TrimRight(v.FloatString(18), "0"), ".")
			if decimal == "" || decimal == "-0" {
				decimal = "0"
			}
			approx, _ := new(big.Rat).SetString(decimal)
			return encodeCommunity(map[string]any{"exact": v.RatString(), "decimal": decimal, "decimal_places": 18, "rounded": approx == nil || approx.Cmp(v) != 0})
		}},
	}
}

// A small parser avoids evaluating arbitrary Go syntax. Node/depth/bit ceilings
// bound both intermediate arithmetic and output before expensive operations.
type arithmetic struct {
	input      string
	pos, nodes int
}

func (p *arithmetic) space() {
	for p.pos < len(p.input) && strings.ContainsRune(" \t\r\n", rune(p.input[p.pos])) {
		p.pos++
	}
}
func (p *arithmetic) expression(min, depth int) (*big.Rat, error) {
	p.nodes++
	if p.nodes > 256 || depth > 48 {
		return nil, domain.ErrInvalid
	}
	p.space()
	var left *big.Rat
	var err error
	if p.pos >= len(p.input) {
		return nil, domain.ErrInvalid
	}
	switch p.input[p.pos] {
	case '+', '-':
		op := p.input[p.pos]
		p.pos++
		left, err = p.expression(3, depth+1)
		if err == nil && op == '-' {
			left.Neg(left)
		}
	case '(':
		p.pos++
		left, err = p.expression(0, depth+1)
		p.space()
		if err == nil && (p.pos >= len(p.input) || p.input[p.pos] != ')') {
			err = domain.ErrInvalid
		}
		if err == nil {
			p.pos++
		}
	default:
		left, err = p.number()
	}
	if err != nil {
		return nil, err
	}
	for {
		p.space()
		if p.pos >= len(p.input) {
			break
		}
		op := p.input[p.pos]
		prec := 0
		switch op {
		case '+', '-':
			prec = 1
		case '*', '/', '%':
			prec = 2
		case '^':
			prec = 4
		default:
			return left, nil
		}
		if prec < min {
			break
		}
		p.pos++
		next := prec + 1
		if op == '^' {
			next = prec
		}
		right, e := p.expression(next, depth+1)
		if e != nil {
			return nil, e
		}
		switch op {
		case '+':
			left.Add(left, right)
		case '-':
			left.Sub(left, right)
		case '*':
			left.Mul(left, right)
		case '/':
			if right.Sign() == 0 {
				return nil, domain.ErrInvalid
			}
			left.Quo(left, right)
		case '%':
			if !left.IsInt() || !right.IsInt() || right.Sign() == 0 {
				return nil, domain.ErrInvalid
			}
			left.SetInt(new(big.Int).Rem(left.Num(), right.Num()))
		case '^':
			if !right.IsInt() || !right.Num().IsInt64() {
				return nil, domain.ErrInvalid
			}
			n := right.Num().Int64()
			if n < -256 || n > 256 || (left.Sign() == 0 && n < 0) {
				return nil, domain.ErrInvalid
			}
			if n < 0 {
				left.Inv(left)
				n = -n
			}
			if int64(left.Num().BitLen()+left.Denom().BitLen())*n > 8192 {
				return nil, domain.ErrInvalid
			}
			left.SetFrac(new(big.Int).Exp(left.Num(), big.NewInt(n), nil), new(big.Int).Exp(left.Denom(), big.NewInt(n), nil))
		}
		if left.Num().BitLen() > 8192 || left.Denom().BitLen() > 8192 {
			return nil, domain.ErrInvalid
		}
	}
	return left, nil
}
func (p *arithmetic) number() (*big.Rat, error) {
	start := p.pos
	digits := 0
	for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
		p.pos++
		digits++
	}
	if p.pos < len(p.input) && p.input[p.pos] == '.' {
		p.pos++
		for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
			p.pos++
			digits++
		}
	}
	if digits == 0 || digits > 256 {
		return nil, domain.ErrInvalid
	}
	if p.pos < len(p.input) && (p.input[p.pos] == 'e' || p.input[p.pos] == 'E') {
		p.pos++
		es := p.pos
		if p.pos < len(p.input) && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
			p.pos++
		}
		ds := p.pos
		for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
			p.pos++
		}
		if ds == p.pos || p.pos-es > 5 {
			return nil, domain.ErrInvalid
		}
		n, err := strconv.Atoi(p.input[es:p.pos])
		if err != nil || n < -1000 || n > 1000 {
			return nil, domain.ErrInvalid
		}
	}
	v, ok := new(big.Rat).SetString(p.input[start:p.pos])
	if !ok || v.Num().BitLen() > 8192 || v.Denom().BitLen() > 8192 {
		return nil, domain.ErrInvalid
	}
	return v, nil
}
