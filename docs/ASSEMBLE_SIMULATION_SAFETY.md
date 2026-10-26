# Simulation result safety

`stellar.Assemble` must validate every simulation response shape before
assembling a transaction. Missing, malformed, or inconsistent simulation data
returns a descriptive error and never produces a transaction that differs from
the simulated result.
