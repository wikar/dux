SELECT surface, COUNT(match_num) AS 'Matches'
FROM matches
GROUP BY surface
