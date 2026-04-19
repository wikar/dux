WITH gs_finals AS (
    SELECT * FROM matches
    WHERE round = 'F' AND tourney_level = 'G'
)
SELECT winner_name, COUNT(match_num) AS 'Titles'
FROM gs_finals
GROUP BY winner_name
