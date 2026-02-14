-- Manual reconciliation query
-- Compares cached account balances against derived balances from ledger entries.
-- Run this against PostgreSQL to verify ledger integrity.

SELECT
    a.id AS account_id,
    a.name AS account_name,
    a.account_type,
    a.balance AS cached_balance,
    COALESCE(credits.total, 0) - COALESCE(debits.total, 0) AS derived_balance,
    CASE
        WHEN a.balance = COALESCE(credits.total, 0) - COALESCE(debits.total, 0)
        THEN 'OK'
        ELSE 'MISMATCH'
    END AS status
FROM accounts a
LEFT JOIN (
    SELECT account_id, SUM(amount) AS total
    FROM ledger_entries
    WHERE entry_type = 'CREDIT'
    GROUP BY account_id
) credits ON credits.account_id = a.id
LEFT JOIN (
    SELECT account_id, SUM(amount) AS total
    FROM ledger_entries
    WHERE entry_type = 'DEBIT'
    GROUP BY account_id
) debits ON debits.account_id = a.id
ORDER BY a.name;

-- Verify that total debits == total credits across the entire ledger (zero-sum check)
SELECT
    SUM(CASE WHEN entry_type = 'DEBIT' THEN amount ELSE 0 END) AS total_debits,
    SUM(CASE WHEN entry_type = 'CREDIT' THEN amount ELSE 0 END) AS total_credits,
    CASE
        WHEN SUM(CASE WHEN entry_type = 'DEBIT' THEN amount ELSE 0 END) =
             SUM(CASE WHEN entry_type = 'CREDIT' THEN amount ELSE 0 END)
        THEN 'BALANCED'
        ELSE 'UNBALANCED'
    END AS ledger_status
FROM ledger_entries;
