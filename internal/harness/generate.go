package harness

import (
	"fmt"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

var railCycle = [...]domain.Rail{domain.RailUPIAutopay, domain.RailENACH}

func GenerateCycles(seed int64, n int) []CycleSpec {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cycles := make([]CycleSpec, n)
	for i := 0; i < n; i++ {
		rail := railCycle[i%len(railCycle)]
		amount := int64(20_000 + (i%25)*20_000)
		dueDate := base.AddDate(0, 0, i%90)
		cycles[i] = CycleSpec{
			CycleID:     domain.CycleID(fmt.Sprintf("harness-seed%d-cycle%d", seed, i)),
			CustomerID:  domain.CustomerID(fmt.Sprintf("harness-seed%d-customer%d", seed, i)),
			Rail:        rail,
			AmountPaise: amount,
			DueDate:     dueDate,
		}
	}
	return cycles
}
