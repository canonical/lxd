test_database_lock() {
    for table in $(echo .tables | sqlite3 ${LXD_DIR}/lxd.db)
    do
        echo "SELECT * FROM $table;" | sqlite3 ${LXD_DIR}/lxd.db >/dev/null
    done
}
