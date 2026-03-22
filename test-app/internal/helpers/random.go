package helpers

import (
	"fmt"
	"math/rand"
	"time"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

// RandomEmail generates a unique email for testing.
func RandomEmail() string {
	return fmt.Sprintf("test-%d-%d@exbanka-test.com", time.Now().UnixNano(), rng.Intn(10000))
}

// RandomJMBG generates a valid 13-digit JMBG.
func RandomJMBG() string {
	return fmt.Sprintf("%013d", rng.Int63n(9000000000000)+1000000000000)
}

// RandomPhone generates a phone number.
func RandomPhone() string {
	return fmt.Sprintf("+3816%08d", rng.Intn(100000000))
}

// RandomName generates a random first or last name.
func RandomName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, rng.Intn(100000))
}

// RandomUsername generates a unique username.
func RandomUsername() string {
	return fmt.Sprintf("user_%d_%d", time.Now().UnixNano(), rng.Intn(10000))
}

// RandomPassword generates a password meeting validation rules (8-32 chars, 2+ digits, 1 upper, 1 lower).
func RandomPassword() string {
	return fmt.Sprintf("Test%02dPass", rng.Intn(100))
}

// RandomAmount generates a random positive decimal string.
func RandomAmount(min, max float64) string {
	val := min + rng.Float64()*(max-min)
	return fmt.Sprintf("%.2f", val)
}

// DateOfBirthUnix returns a Unix timestamp for a date of birth (age 25-50).
func DateOfBirthUnix() int64 {
	age := 25 + rng.Intn(25)
	return time.Now().AddDate(-age, 0, 0).Unix()
}
