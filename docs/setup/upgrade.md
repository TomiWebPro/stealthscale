# Upgrade an existing installation

!!! tip "Required update path"

    It's required to update from one stable version to the next (e.g. 0.26.0 → 0.27.1 → 0.28.0) without skipping minor
    versions in between. You should always pick the latest available patch release.

Update an existing StealthScale installation to a new version:

- Read the announcement on the [GitHub releases](https://github.com/tomiwebpro/stealthscale/releases) page for the new
  version. It lists the changes of the release along with possible breaking changes and version-specific upgrade
  instructions.
- Stop StealthScale
- **[Create a backup of your installation](#backup)**
- Update StealthScale to the new version, preferably by following the same installation method.
- Compare and update the [configuration](../ref/configuration.md) file.
- Start StealthScale

## Backup

StealthScale applies database migrations during upgrades and we highly recommend to create a backup of your database before
upgrading. A full backup of StealthScale depends on your individual setup, but below are some typical setup scenarios.

=== "Standard installation"

    An installation that follows our [official releases](install/official.md) setup guide uses the following paths:

    - [Configuration file](../ref/configuration.md): `/etc/stealthscale/config.yaml`
    - Data directory: `/var/lib/stealthscale`
    - SQLite as database: `/var/lib/stealthscale/db.sqlite`

    ```console
    TIMESTAMP=$(date +%Y%m%d%H%M%S)
    cp -aR /etc/stealthscale /etc/stealthscale.backup-$TIMESTAMP
    cp -aR /var/lib/stealthscale /var/lib/stealthscale.backup-$TIMESTAMP
    ```

=== "Container"

    An installation that follows our [container](install/container.md) setup guide uses a single source volume directory
    that contains the configuration file, data directory and the SQLite database.

    ```console
    cp -aR /path/to/stscale /path/to/stscale.backup-$(date +%Y%m%d%H%M%S)
    ```

=== "PostgreSQL"

    Please follow PostgreSQL's [Backup and Restore](https://www.postgresql.org/docs/current/backup.html) documentation
    to create a backup of your PostgreSQL database.
