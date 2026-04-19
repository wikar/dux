WITH clay AS (
    SELECT * FROM matches
    WHERE surface = 'Clay'
),
clay_wins AS (
    SELECT winner_name, COUNT(match_num) AS 'Wins'
    FROM clay
    GROUP BY winner_name
)
SELECT * FROM clay_wins
ORDER BY "Wins" DESC
LIMIT 10
