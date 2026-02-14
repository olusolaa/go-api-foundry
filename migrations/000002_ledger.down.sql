DROP TRIGGER IF EXISTS trg_ledger_entries_immutable ON ledger_entries;
DROP FUNCTION IF EXISTS prevent_ledger_entry_mutation();
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS accounts;
