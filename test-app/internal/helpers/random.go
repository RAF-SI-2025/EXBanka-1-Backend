package helpers

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync/atomic"
	"time"
)

// seq is a monotonic counter used to disambiguate otherwise-colliding values
// across parallel tests. Two goroutines calling RandomEmail at the same
// nanosecond would otherwise generate identical emails, tripping the unique
// constraint on employees.email; the counter guarantees uniqueness.
var seq uint64

func nextSeq() uint64 { return atomic.AddUint64(&seq, 1) }

// RandomEmail generates a unique email for testing.
// Uses +test tag so notification-service skips actual SMTP delivery.
func RandomEmail() string {
	return fmt.Sprintf("exbanka+test-%d-%d@exbanka-test.com", time.Now().UnixNano(), nextSeq())
}

// RandomJMBG generates a valid 13-digit JMBG.
func RandomJMBG() string {
	return fmt.Sprintf("%013d", rand.Int63n(9000000000000)+1000000000000)
}

// RandomPhone generates a phone number.
func RandomPhone() string {
	return fmt.Sprintf("+3816%08d", rand.Intn(100000000))
}

// RandomName generates a random first or last name.
func RandomName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, rand.Intn(100000))
}

// RandomUsername generates a unique username.
func RandomUsername() string {
	return fmt.Sprintf("user_%d_%d", time.Now().UnixNano(), nextSeq())
}

// RandomPassword generates a password meeting validation rules (8-32 chars, 2+ digits, 1 upper, 1 lower).
func RandomPassword() string {
	return fmt.Sprintf("Test%02dPass", rand.Intn(100))
}

// RandomAmount generates a random positive decimal string.
func RandomAmount(min, max float64) string {
	val := min + rand.Float64()*(max-min)
	return fmt.Sprintf("%.2f", val)
}

// FormatID converts an int to its string representation for URL paths.
func FormatID(id int) string {
	return strconv.Itoa(id)
}

// DateOfBirthUnix returns a Unix timestamp for a date of birth (age 25-50).
func DateOfBirthUnix() int64 {
	age := 25 + rand.Intn(25)
	return time.Now().AddDate(-age, 0, 0).Unix()
}
