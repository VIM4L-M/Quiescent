package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

type OracleSlot struct {
	At            time.Time `json:"at"`
	BalancePaise  int64     `json:"balancePaise"`
	Blocked       bool      `json:"blocked"`
	Probabilities []float64 `json:"probabilities"`
}

type OracleResponse struct {
	CycleID     domain.CycleID    `json:"cycleID"`
	CustomerID  domain.CustomerID `json:"customerID"`
	Seed        int64             `json:"seed"`
	Rail        domain.Rail       `json:"rail"`
	AmountPaise int64             `json:"amountPaise"`
	Revoked     bool              `json:"revoked"`
	MaxAttempts int               `json:"maxAttempts"`
	Slots       []OracleSlot      `json:"slots"`
}

type OracleQuery struct {
	CycleID     domain.CycleID
	Rail        domain.Rail
	AmountPaise int64
	From        time.Time
	Days        int
	StepMinutes int
}

func (s *Sim) handleOracle(w http.ResponseWriter, r *http.Request) {
	q, err := parseOracleQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeMalformedRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.Probabilities(q))
}

func parseOracleQuery(v url.Values) (OracleQuery, error) {
	q := OracleQuery{
		CycleID:     domain.CycleID(v.Get("cycleID")),
		Rail:        domain.Rail(v.Get("rail")),
		Days:        7,
		StepMinutes: 60,
	}
	if q.CycleID == "" {
		return q, fmt.Errorf("cycleID is required")
	}
	if s := v.Get("amountPaise"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return q, fmt.Errorf("amountPaise: %w", err)
		}
		q.AmountPaise = n
	}
	if s := v.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return q, fmt.Errorf("from: %w", err)
		}
		q.From = t
	}
	if s := v.Get("days"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 90 {
			return q, fmt.Errorf("days must be between 1 and 90")
		}
		q.Days = n
	}
	if s := v.Get("stepMinutes"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 1440 {
			return q, fmt.Errorf("stepMinutes must be between 1 and 1440")
		}
		q.StepMinutes = n
	}
	return q, nil
}

func (s *Sim) Probabilities(q OracleQuery) OracleResponse {
	cycle := s.world.Cycle(q.CycleID)
	rail := q.Rail
	if rail == "" {
		rail = cycle.Rail
	}
	amount := q.AmountPaise
	if amount == 0 {
		amount = cycle.AmountPaise
	}
	from := q.From
	if from.IsZero() {
		from = cycle.DueDate
	}
	if from.IsZero() {
		from = s.now()
	}
	revoked := s.ledger.Revoked(q.CycleID)
	maxAttempts := int(domain.MaxAttempts)

	step := time.Duration(q.StepMinutes) * time.Minute
	end := from.Add(time.Duration(q.Days) * 24 * time.Hour)

	resp := OracleResponse{
		CycleID:     q.CycleID,
		CustomerID:  cycle.CustomerID,
		Seed:        s.cfg.Seed,
		Rail:        rail,
		AmountPaise: amount,
		Revoked:     revoked,
		MaxAttempts: maxAttempts,
	}
	for t := from; t.Before(end); t = t.Add(step) {
		slot := OracleSlot{
			At:            t,
			BalancePaise:  s.world.BalanceAt(q.CycleID, t),
			Blocked:       Blocked(t),
			Probabilities: make([]float64, maxAttempts),
		}
		for n := 1; n <= maxAttempts; n++ {
			slot.Probabilities[n-1] = SuccessProbability(Conditions{
				Seed:           s.cfg.Seed,
				CycleID:        q.CycleID,
				AttemptNumber:  n,
				Rail:           rail,
				AmountPaise:    amount,
				BalancePaise:   slot.BalancePaise,
				FiredAt:        t,
				MandateRevoked: revoked,
			})
		}
		resp.Slots = append(resp.Slots, slot)
	}
	return resp
}

type OracleClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewOracleClient(baseURL string, timeout time.Duration) *OracleClient {
	return &OracleClient{BaseURL: baseURL, HTTP: &http.Client{Timeout: timeout}}
}

func (c *OracleClient) Probabilities(ctx context.Context, q OracleQuery) (OracleResponse, error) {
	v := url.Values{}
	v.Set("cycleID", string(q.CycleID))
	if q.Rail != "" {
		v.Set("rail", string(q.Rail))
	}
	if q.AmountPaise != 0 {
		v.Set("amountPaise", strconv.FormatInt(q.AmountPaise, 10))
	}
	if !q.From.IsZero() {
		v.Set("from", q.From.Format(time.RFC3339))
	}
	if q.Days != 0 {
		v.Set("days", strconv.Itoa(q.Days))
	}
	if q.StepMinutes != 0 {
		v.Set("stepMinutes", strconv.Itoa(q.StepMinutes))
	}

	var out OracleResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+"/oracle/probabilities?"+v.Encode(), nil)
	if err != nil {
		return out, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("provider: oracle returned %d", resp.StatusCode)
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}
