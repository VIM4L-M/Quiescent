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

const cyclesPerCustomer = 6

func GenerateCustomerSequences(seed int64, customers int) [][]CycleSpec {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sequences := make([][]CycleSpec, customers)
	for cust := 0; cust < customers; cust++ {
		customerID := domain.CustomerID(fmt.Sprintf("harness-seed%d-customer%d", seed, cust))
		rail := railCycle[cust%len(railCycle)]
		amount := int64(20_000 + (cust%25)*20_000)
		seq := make([]CycleSpec, cyclesPerCustomer)
		for m := 0; m < cyclesPerCustomer; m++ {
			seq[m] = CycleSpec{
				CycleID:     domain.CycleID(fmt.Sprintf("harness-seed%d-customer%d-cycle%d", seed, cust, m)),
				CustomerID:  customerID,
				Rail:        rail,
				AmountPaise: amount,
				DueDate:     base.AddDate(0, m, cust%28),
			}
		}
		sequences[cust] = seq
	}
	return sequences
}
