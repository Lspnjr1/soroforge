# `soroforge init` test matrix

The init command preserves existing configuration by default, replaces it only with `--force`, honors `--dir`, and reports a safe placeholder when no artifacts exist. These cases belong at the command boundary in addition to scaffold tests.
