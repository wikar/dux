-- ============================================================================
-- Date dimension build script for DuckDB (common.db)
-- Base table (base.Date) holds only DateKey + Date; the default-schema view
-- "Date" derives everything else on top of it.
--
-- Run with:
--   duckdb.exe common.db -f create_date_dimension.sql
-- ============================================================================

CREATE SCHEMA IF NOT EXISTS base;

-- ----------------------------------------------------------------------------
-- 1) Base Date dimension table  (DateKey + Date only)
-- ----------------------------------------------------------------------------
DROP VIEW  IF EXISTS "Date";
DROP TABLE IF EXISTS base."Date";

CREATE TABLE base."Date" (
    DateKey INTEGER   NOT NULL,   -- YYYYMMDD, e.g. 20260601
    Date    TIMESTAMP NOT NULL,
    PRIMARY KEY (DateKey)
);

-- ----------------------------------------------------------------------------
-- 2) Populate every day 2000-01-01 .. 2040-12-31, plus a -1 "unknown" member
-- ----------------------------------------------------------------------------
INSERT INTO base."Date" (DateKey, Date)
SELECT CAST(strftime(d, '%Y%m%d') AS INTEGER),
       CAST(d AS TIMESTAMP)
FROM generate_series(DATE '2000-01-01', DATE '2040-12-31', INTERVAL '1 day') AS t(d);

-- Unknown/unmatched member for fact rows without a real date.
INSERT INTO base."Date" (DateKey, Date) VALUES (-1, TIMESTAMP '1900-01-01');

-- ----------------------------------------------------------------------------
-- 3) "Date" view (default schema) - derives all attributes from base.Date
-- ----------------------------------------------------------------------------
CREATE OR REPLACE VIEW "Date" AS
WITH base AS (
    SELECT
        DateKey,
        Date,
        CAST(date_part('year',    Date) AS INTEGER) AS Year,
        CAST(date_part('quarter', Date) AS INTEGER) AS QuarterNo,
        CAST(date_part('month',   Date) AS INTEGER) AS MonthNo_i,
        CAST(date_part('week',    Date) AS INTEGER) AS IsoWeek,   -- ISO 8601 week number
        -- Week label uses ISO-year (not calendar year) so days that fall in
        -- ISO week 1 but calendar Dec (e.g. 2025-12-29) label as next year's
        -- week and sort correctly across the year boundary.
        CAST(date_part('isoyear', Date) AS VARCHAR)
            || '-W' || LPAD(CAST(date_part('week', Date) AS VARCHAR), 2, '0') AS Week
    FROM base."Date"
),
-- Running numbers computed over REAL dates only (-1 excluded), joined back by
-- DateKey. The -1 row therefore gets NULL ranks, and "today" lands on 0 for
-- every *RelativeCurrent column (consistent with YearNumberRelativeCurrent,
-- which is a plain year subtraction).
ranks AS (
    SELECT DateKey,
           DENSE_RANK() OVER (ORDER BY Year)              AS YearNumberRunning,
           DENSE_RANK() OVER (ORDER BY Year, QuarterNo)   AS QuarterNumberRunning,
           DENSE_RANK() OVER (ORDER BY Year, MonthNo_i)   AS MonthNumberRunning,
           DENSE_RANK() OVER (ORDER BY Week)              AS WeekNumberRunning,
           DENSE_RANK() OVER (ORDER BY Date)              AS DateNumberRunning
    FROM base
    WHERE DateKey <> -1
),
today_ranks AS (
    SELECT r.YearNumberRunning, r.QuarterNumberRunning, r.MonthNumberRunning,
           r.WeekNumberRunning, r.DateNumberRunning
    FROM ranks r
    JOIN base b ON b.DateKey = r.DateKey
    WHERE b.Date = CURRENT_DATE
),
-- Precompute today's WeekQuarter and the WeekQuarter 13 weeks (91 days) back,
-- so Current/PreviousWeekQuarter are plain string comparisons.
today_wq AS (
    SELECT LEFT(Week, 4) || '-WQ' ||
           CASE WHEN IsoWeek = 53 THEN '4'
                ELSE CAST(((IsoWeek - 1) // 13) + 1 AS VARCHAR) END AS TodayWeekQuarter
    FROM base WHERE Date = CURRENT_DATE
),
prev_wq AS (
    SELECT LEFT(Week, 4) || '-WQ' ||
           CASE WHEN IsoWeek = 53 THEN '4'
                ELSE CAST(((IsoWeek - 1) // 13) + 1 AS VARCHAR) END AS PrevWeekQuarter
    FROM base WHERE Date = CURRENT_DATE - INTERVAL '91 day'
)
SELECT
    d.DateKey,
    d.Year,
    CAST(d.Year AS VARCHAR) || '-Q' || CAST(d.QuarterNo AS VARCHAR)           AS Quarter,       -- e.g. 2026-Q1
    'Q' || CAST(d.QuarterNo AS VARCHAR)                                       AS QuarterName,   -- e.g. Q1
    d.MonthNo_i                                                              AS Month,
    strftime(d.Date, '%B')                                                    AS MonthName,
    LPAD(CAST(d.MonthNo_i AS VARCHAR), 2, '0')                                AS MonthNo,
    LEFT(d.Week, 4) || '-WQ' ||
        CASE WHEN d.IsoWeek = 53 THEN '4'
             ELSE CAST(((d.IsoWeek - 1) // 13) + 1 AS VARCHAR) END            AS WeekQuarter,
    d.Week,                                                                                     -- e.g. 2026-W03
    LPAD(CAST(d.IsoWeek AS VARCHAR), 2, '0')                                  AS WeekNo,
    d.Date,
    CASE WHEN d.DateKey = -1 THEN TIMESTAMP '1900-01-01'
         ELSE d.Date - INTERVAL '364 day' END                                 AS ComparisonDatePY,
    CASE WHEN d.DateKey = -1 THEN -1
         ELSE CAST(strftime(d.Date - INTERVAL '364 day', '%Y%m%d') AS INTEGER) END AS ComparisonDatePYKey,
    r.YearNumberRunning,
    r.QuarterNumberRunning,
    r.MonthNumberRunning,
    r.WeekNumberRunning,
    r.DateNumberRunning,
    r.QuarterNumberRunning - tr.QuarterNumberRunning                          AS QuarterNumberRelativeCurrent,
    r.MonthNumberRunning   - tr.MonthNumberRunning                            AS MonthNumberRelativeCurrent,
    r.WeekNumberRunning    - tr.WeekNumberRunning                             AS WeekNumberRelativeCurrent,
    r.DateNumberRunning    - tr.DateNumberRunning                             AS DateNumberRelativeCurrent,
    d.Year - CAST(date_part('year', CURRENT_DATE) AS INTEGER)                 AS YearNumberRelativeCurrent,
    CASE WHEN d.Date = CURRENT_DATE THEN 'Yes' ELSE 'No' END                  AS Today,
    CASE WHEN d.Date = CURRENT_DATE - INTERVAL '1 day' THEN 'Yes' ELSE 'No' END AS Yesterday,
    CASE WHEN d.Date = CURRENT_DATE + INTERVAL '1 day' THEN 'Yes' ELSE 'No' END AS Tomorrow,
    CASE WHEN d.Date < CURRENT_DATE THEN 'Yes' ELSE 'No' END                  AS EarlierThanToday,
    CASE WHEN d.Date > CURRENT_DATE THEN 'Yes' ELSE 'No' END                  AS LaterThanToday,
    CASE WHEN date_trunc('week', d.Date) = date_trunc('week', CURRENT_DATE)
         THEN 'Yes' ELSE 'No' END                                             AS CurrentWeek,
    CASE WHEN date_trunc('week', d.Date) = date_trunc('week', CURRENT_DATE) - INTERVAL '7 day'
         THEN 'Yes' ELSE 'No' END                                             AS PreviousWeek,
    CASE WHEN d.Year = CAST(date_part('year', CURRENT_DATE) AS INTEGER)
          AND d.MonthNo_i = CAST(date_part('month', CURRENT_DATE) AS INTEGER)
         THEN 'Yes' ELSE 'No' END                                             AS CurrentMonth,
    CASE WHEN date_trunc('month', d.Date) = date_trunc('month', CURRENT_DATE) - INTERVAL '1 month'
         THEN 'Yes' ELSE 'No' END                                             AS PreviousMonth,
    CASE WHEN d.Year = CAST(date_part('year', CURRENT_DATE) AS INTEGER)
          AND d.QuarterNo = CAST(date_part('quarter', CURRENT_DATE) AS INTEGER)
         THEN 'Yes' ELSE 'No' END                                            AS CurrentQuarter,
    CASE WHEN date_trunc('quarter', d.Date) = date_trunc('quarter', CURRENT_DATE) - INTERVAL '3 month'
         THEN 'Yes' ELSE 'No' END                                            AS PreviousQuarter,
    CASE WHEN (LEFT(d.Week, 4) || '-WQ' ||
                CASE WHEN d.IsoWeek = 53 THEN '4'
                     ELSE CAST(((d.IsoWeek - 1) // 13) + 1 AS VARCHAR) END) = twq.TodayWeekQuarter
         THEN 'Yes' ELSE 'No' END                                             AS CurrentWeekQuarter,
    CASE WHEN (LEFT(d.Week, 4) || '-WQ' ||
                CASE WHEN d.IsoWeek = 53 THEN '4'
                     ELSE CAST(((d.IsoWeek - 1) // 13) + 1 AS VARCHAR) END) = pwq.PrevWeekQuarter
         THEN 'Yes' ELSE 'No' END                                             AS PreviousWeekQuarter,
    CASE WHEN d.Year = CAST(date_part('year', CURRENT_DATE) AS INTEGER) THEN 'Yes' ELSE 'No' END AS CurrentYear,
    CASE WHEN d.Year = CAST(date_part('year', CURRENT_DATE) AS INTEGER) - 1 THEN 'Yes' ELSE 'No' END AS PreviousYear,
    CAST(date_part('isodow', d.Date) AS VARCHAR)                              AS DayOfWeekNo,   -- Monday=1 .. Sunday=7
    strftime(d.Date, '%A')                                                    AS DayOfWeek,
    CAST(date_part('day', d.Date) AS INTEGER)                                 AS DayOfMonth,
    CASE WHEN strftime(d.Date, '%A') IN ('Friday', 'Saturday', 'Sunday') THEN 'Yes' ELSE 'No' END AS Weekend
FROM base d
LEFT JOIN ranks r        ON r.DateKey = d.DateKey
LEFT JOIN today_ranks tr ON TRUE
LEFT JOIN today_wq   twq ON TRUE
LEFT JOIN prev_wq    pwq ON TRUE
;
