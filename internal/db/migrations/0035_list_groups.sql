-- Who gets what a list adds.
--
-- A list entitled nobody. Every sync filled a library with titles no
-- shared account could watch until somebody tagged them by hand, which
-- for a chart that refreshes twice a day is not a thing anybody keeps up
-- with. The audience belongs on the list because that is where the
-- decision is: "everything Trending adds is for the house" is one
-- choice, not one per title.
--
-- Empty is the existing behaviour and stays the default, so upgrading
-- grants nobody anything. Access is never widened by a migration.
--
-- It applies to what a list adds AFTER the audience is set. Nothing
-- records which titles a given list added, and inferring it would
-- overwrite tagging done by hand on titles that merely happen to be on a
-- chart.
CREATE TABLE list_groups (
    list_id  INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    group_id INTEGER NOT NULL REFERENCES share_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (list_id, group_id)
);
CREATE INDEX idx_list_groups_group ON list_groups(group_id);
