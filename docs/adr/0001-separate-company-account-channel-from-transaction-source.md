# Separate Company Account Channel from Transaction Source

Company accounts and ledger movements use different classifications: `OTHER` is an Account Channel for accounts without a Safeheron or Airwallex integration, while `MANUAL` remains the Transaction Source for operator-entered movements. Monera Digital must recognize enabled Other Accounts without registering them for provider collection, Webhook matching, reconciliation, asset policies, risk enrichment, or automatic valuation. This separation prevents a manually maintained bank or external account from being mistaken for a provider transaction source while still allowing finance to attribute Manual Transactions to it.

## Considered Options

- Reuse `MANUAL` as an account channel: rejected because it conflates how an account is integrated with how a transaction entered the ledger.
- Add `OTHER` to the existing general transaction-channel validity rule: rejected because that rule is consumed by provider events, synchronization, risk, valuation, and transaction persistence paths.
- Keep Other Accounts disabled to avoid runtime changes: rejected because finance must be able to use a newly created Other Account immediately for manual transaction entry.

## Consequences

- Account-channel validation and transaction-source validation must remain distinct in code.
- Other Accounts receive an internal immutable account key but expose only finance-recognizable business fields.
- The shared database permits `OTHER` only for company accounts; company-fund transaction channels remain unchanged.
