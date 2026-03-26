package shared

import "errors"

// ErrOptimisticLock is returned when a concurrent modification is detected.
var ErrOptimisticLock = errors.New("optimistic lock conflict: record was modified by another transaction")
