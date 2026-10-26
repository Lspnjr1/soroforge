# Deployer key generation

Key generation should write a newly generated deployer seed to the requested keystore path with owner-only permissions. Existing files require an explicit force flag, and the command must never print the seed or include it in logs.
