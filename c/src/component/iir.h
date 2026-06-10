// IIR coefficient library lookup (internal to the filter component).
#ifndef ONDES_IIR_H
#define ONDES_IIR_H

#include <stddef.h>

// iir_spec looks up a coefficient set by key (e.g. "lp_6_1k"). On success
// it points *a/*b at static coefficient arrays of length *na/*nb and
// returns true; on an unknown key it returns false.
bool iir_spec(const char *key, const double **a, size_t *na, const double **b,
              size_t *nb);

#endif // ONDES_IIR_H
