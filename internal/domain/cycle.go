package domain

import "time"

const MaxAttempts int16 = 4

type MandateCycle struct {
	CycleID      CycleID    `db:"cycle_id"`
	MandateID    MandateID  `db:"mandate_id"`
	CustomerID   CustomerID `db:"customer_id"`
	Rail         Rail       `db:"rail"`
	AmountPaise  int64      `db:"amount_paise"`
	DueDate      time.Time  `db:"due_date"`
	AttemptsUsed int16      `db:"attempts_used"`
	State        State      `db:"state"`
	Version      int64      `db:"version"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

func (c MandateCycle) Disposition() *Disposition {
	if !c.State.Terminal() {
		return nil
	}
	d := Disposition(c.State)
	return &d
}

func (c MandateCycle) BudgetRemaining() int16 {
	if c.AttemptsUsed >= MaxAttempts {
		return 0
	}
	return MaxAttempts - c.AttemptsUsed
}
