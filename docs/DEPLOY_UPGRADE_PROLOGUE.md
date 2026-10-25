# Deploy and upgrade prologue

Deploy and upgrade share signer resolution, artifact validation, structured
logging, fee accounting, and failed-attempt recording. Changes to one path must
be made through the shared prologue so checks cannot drift between new deploys
and upgrades.
