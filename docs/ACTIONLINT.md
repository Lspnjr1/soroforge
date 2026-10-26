# Workflow linting

Pull requests should run `actionlint` over every workflow under `.github/workflows` so invalid expressions, event filters, and `needs` references fail before a release or security workflow is needed. The check is local and does not require credentials.
