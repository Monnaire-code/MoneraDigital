# Company Funds

The Company Funds context describes company-owned financial accounts and their ledger movements, regardless of whether activity is collected automatically or entered by finance.

## Accounts

**Company Account**:
A financial account owned or controlled by a company entity and available for company-fund ledger attribution.
_Avoid_: Wallet when the account may be a bank or other non-wallet account

**Account Channel**:
The integration kind of a Company Account: Safeheron, Airwallex, or Other Account. It describes how the account is managed, not how an individual transaction entered the ledger.
_Avoid_: Channel without qualification, Transaction Source

**Provider-managed Account**:
A Company Account connected to Safeheron or Airwallex and eligible for provider-specific ingestion and reconciliation rules.
_Avoid_: Automatic Account

**Other Account**:
A Company Account maintained for manual finance bookkeeping without a Safeheron or Airwallex integration. It may represent a bank account or another externally managed account.
_Avoid_: Manual Account, because manual describes transaction entry rather than account identity

**Enabled Account**:
A Company Account eligible for new activity. A disabled account remains available for historical attribution but cannot be selected for a new Manual Transaction.
_Avoid_: Active Account

**Account Display Name**:
A finance-recognizable label for a Company Account, normally including institution, currency or purpose, and identifying tail digits where useful.
_Avoid_: Provider Account Key, full bank account number

**Provider Account Key**:
An immutable technical identifier used to distinguish a Company Account. For an Other Account it is generated internally and is not entered or shown as a business field.
_Avoid_: Account Display Name

**Asset Policy**:
Provider-specific configuration governing automated asset recognition, dust handling, and valuation behavior for a Provider-managed Account.
_Avoid_: Currency list for Other Accounts

## Transactions

**Transaction Source**:
The origin of a ledger movement, such as Safeheron, Airwallex, or Manual. It is independent of the referenced Account Channel.
_Avoid_: Account Channel

**Manual Transaction**:
A company-fund ledger movement entered by an authorized finance operator and recorded with the Manual Transaction Source. It may reference any Enabled Account, including an Other Account.
_Avoid_: Other Transaction
