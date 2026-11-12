//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopanel/internal/sys"
)

var identifierRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// mysqlToolFallbacks covers hosts where PATH is minimal (systemd) or the
// client comes from a tarball install that is not linked into /usr/bin.
var mysqlToolFallbacks = map[string][]string{
	"mysql":        {"/usr/bin/mysql", "/usr/local/bin/mysql", "/usr/local/mysql/bin/mysql", "/opt/mysql/bin/mysql"},
	"mariadb":      {"/usr/bin/mariadb", "/usr/local/bin/mariadb"},
	"mysqldump":    {"/usr/bin/mysqldump", "/usr/local/bin/mysqldump", "/usr/local/mysql/bin/mysqldump", "/opt/mysql/bin/mysqldump"},
	"mariadb-dump": {"/usr/bin/mariadb-dump", "/usr/local/bin/mariadb-dump"},
}

// findMySQLTool returns the first usable binary among names, checking PATH
// first and then the well-known install locations.
func findMySQLTool(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
		for _, path := range mysqlToolFallbacks[name] {
			info, err := os.Stat(path)
			if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return path
			}
		}
	}
	return ""
}

// mysqlClient resolves the CLI used for SQL statements and restores. MariaDB
// installs expose the same interface as "mariadb", and some minimal images
// ship only that name, so both are accepted.
func mysqlClient() (string, error) {
	if path := findMySQLTool("mysql", "mariadb"); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("%w: install the mysql-client or mariadb-client package", sys.ErrDatabaseClientMissing)
}

// mysqlDumpTool resolves the dump CLI, which MariaDB also ships as
// "mariadb-dump".
func mysqlDumpTool() (string, error) {
	if path := findMySQLTool("mysqldump", "mariadb-dump"); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("%w: install the mysql-client or mariadb-client package", sys.ErrDatabaseClientMissing)
}

func (s *System) mysqlDefaults() (string, func(), error) {
	dir, err := os.MkdirTemp("", "gopanel-mysql-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	var b strings.Builder
	b.WriteString("[client]\n")
	b.WriteString("user=" + s.cfg.MySQLAdminUser + "\n")
	if s.cfg.MySQLAdminPassword != "" {
		b.WriteString("password=" + s.cfg.MySQLAdminPassword + "\n")
	}
	if s.cfg.MySQLSocket != "" {
		b.WriteString("socket=" + s.cfg.MySQLSocket + "\n")
	}
	path := filepath.Join(dir, "client.cnf")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func (s *System) runMySQL(ctx context.Context, query string, timeout time.Duration) (sys.Result, error) {
	client, err := mysqlClient()
	if err != nil {
		return sys.Result{}, err
	}
	cnf, cleanup, err := s.mysqlDefaults()
	if err != nil {
		return sys.Result{}, err
	}
	defer cleanup()
	return s.Run(ctx, client, []string{"--defaults-extra-file=" + cnf, "--batch", "--skip-column-names", "--raw"}, sys.ExecOpts{Stdin: []byte(query), Timeout: timeout, MaxOutput: 4 << 20})
}

func sqlString(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `''`)
	return "'" + v + "'"
}

func sqlIdent(v string) (string, error) {
	if !identifierRe.MatchString(v) {
		return "", errors.New("invalid identifier")
	}
	return "`" + v + "`", nil
}

func (s *System) MySQLListDatabases(ctx context.Context) ([]sys.DBInfo, error) {
	query := `SELECT s.schema_name, COALESCE(SUM(t.data_length+t.index_length),0), COUNT(t.table_name)
FROM information_schema.schemata s
LEFT JOIN information_schema.tables t ON t.table_schema=s.schema_name
WHERE s.schema_name NOT IN ('information_schema','performance_schema','mysql','sys')
GROUP BY s.schema_name ORDER BY s.schema_name;`
	res, err := s.runMySQL(ctx, query, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var out []sys.DBInfo
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		size, _ := strconv.ParseInt(fields[1], 10, 64)
		tables, _ := strconv.Atoi(fields[2])
		out = append(out, sys.DBInfo{Name: fields[0], SizeBytes: size, Tables: tables})
	}
	return out, nil
}

func (s *System) MySQLCreateDatabase(ctx context.Context, name string) error {
	ident, err := sqlIdent(name)
	if err != nil {
		return err
	}
	_, err = s.runMySQL(ctx, "CREATE DATABASE "+ident+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", 30*time.Second)
	return err
}

func (s *System) MySQLDropDatabase(ctx context.Context, name string) error {
	ident, err := sqlIdent(name)
	if err != nil {
		return err
	}
	_, err = s.runMySQL(ctx, "DROP DATABASE "+ident+";", 30*time.Second)
	return err
}

func (s *System) MySQLListUsers(ctx context.Context) ([]sys.DBUser, error) {
	query := `SELECT user, host FROM mysql.user WHERE user NOT IN ('root','mysql.sys','mysql.session','mysql.infoschema','debian-sys-maint') ORDER BY user;`
	res, err := s.runMySQL(ctx, query, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var out []sys.DBUser
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		out = append(out, sys.DBUser{User: fields[0], Host: fields[1]})
	}
	return out, nil
}

func (s *System) MySQLCreateDatabaseUser(ctx context.Context, user, password, database string) error {
	ident, err := sqlIdent(database)
	if err != nil {
		return err
	}
	if !identifierRe.MatchString(user) {
		return errors.New("invalid user")
	}
	account := sqlString(user) + "@'localhost'"
	query := fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s;\nALTER USER %s IDENTIFIED BY %s;\nGRANT ALL PRIVILEGES ON %s.* TO %s;\nFLUSH PRIVILEGES;\n",
		account, sqlString(password), account, sqlString(password), ident, account)
	_, err = s.runMySQL(ctx, query, 30*time.Second)
	return err
}

func (s *System) MySQLDropUser(ctx context.Context, user string) error {
	if !identifierRe.MatchString(user) {
		return errors.New("invalid user")
	}
	_, err := s.runMySQL(ctx, "DROP USER IF EXISTS "+sqlString(user)+"@'localhost';", 30*time.Second)
	return err
}

func (s *System) MySQLSetPassword(ctx context.Context, user, password string) error {
	if !identifierRe.MatchString(user) {
		return errors.New("invalid user")
	}
	_, err := s.runMySQL(ctx, "ALTER USER "+sqlString(user)+"@'localhost' IDENTIFIED BY "+sqlString(password)+"; FLUSH PRIVILEGES;", 30*time.Second)
	return err
}

func (s *System) MySQLDump(ctx context.Context, database string, w io.Writer) error {
	if !identifierRe.MatchString(database) {
		return errors.New("invalid database")
	}
	dump, err := mysqlDumpTool()
	if err != nil {
		return err
	}
	cnf, cleanup, err := s.mysqlDefaults()
	if err != nil {
		return err
	}
	defer cleanup()
	args := []string{"--defaults-extra-file=" + cnf, "--single-transaction", "--quick", "--routines", "--events", "--databases", database}
	_, err = s.RunIO(ctx, dump, args, sys.ExecOpts{Timeout: 30 * time.Minute, MaxOutput: 1 << 20}, w, nil)
	return err
}

func (s *System) MySQLRestore(ctx context.Context, database string, r io.Reader) error {
	if !identifierRe.MatchString(database) {
		return errors.New("invalid database")
	}
	client, err := mysqlClient()
	if err != nil {
		return err
	}
	cnf, cleanup, err := s.mysqlDefaults()
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = s.Run(ctx, client, []string{"--defaults-extra-file=" + cnf, "--database=" + database}, sys.ExecOpts{StdinReader: r, Timeout: 30 * time.Minute, MaxOutput: 1 << 20})
	return err
}
