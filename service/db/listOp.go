package db

import (
	"database/sql"
	"fmt"
	"strings"

	jsoniter "github.com/json-iterator/go"
	"github.com/tidwall/gjson"
)

// subRowJSON is the JSON shape of a subscription row reconstructed from the
// subscriptions table columns.
type subRowJSON struct {
	DatabaseID      int64               `json:"databaseID"`
	Remarks         string              `json:"remarks,omitempty"`
	Address         string              `json:"address"`
	Status          string              `json:"status"`
	Info            string              `json:"info"`
	Servers         jsoniter.RawMessage `json:"servers"`
	AutoSelect      bool                `json:"autoSelect"`
	DecryptPassword string              `json:"decryptPassword,omitempty"`
	Extra           jsoniter.RawMessage `json:"extra,omitempty"`
}

// subExtraFromJSON extracts the "extra" sub-object from a marshaled
// subscription JSON, defaulting to "{}" when absent.
func subExtraFromJSON(b []byte) string {
	parsed := gjson.ParseBytes(b)
	if extra := parsed.Get("extra"); extra.Exists() {
		return extra.Raw
	}
	return "{}"
}

// ListSet sets an element at a specific index in a list.
func ListSet(bucket string, key string, index int, val interface{}) (err error) {
	db := GetDB()

	switch bucket + "/" + key {
	case "touch/servers":
		b, err := jsoniter.Marshal(val)
		if err != nil {
			return err
		}
		result, err := db.Exec(
			"UPDATE servers SET config_json = ?, updated_at = CURRENT_TIMESTAMP WHERE type = 'server' AND sort = ?",
			string(b), index,
		)
		if err != nil {
			return err
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			return fmt.Errorf("ListSet: server at index %d not found", index)
		}
		return nil

	case "touch/subscriptions":
		return SetSubscriptionAndState(index, val, nil)

	default:
		return fmt.Errorf("ListSet: unsupported bucket/key: %s/%s", bucket, key)
	}
}

// SetSubscriptionAndState publishes the new rows and every affected outbound
// membership in one transaction. Membership IDs refer to positions in those rows.
func SetSubscriptionAndState(index int, val any, state map[string]any) error {
	b, err := jsoniter.Marshal(val)
	if err != nil {
		return err
	}
	return ReadModifyWrite(func(tx *sql.Tx) error {
		parsed := gjson.ParseBytes(b)
		var subID int64
		if err := tx.QueryRow("SELECT id FROM subscriptions WHERE sort = ?", index).Scan(&subID); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("ListSet: subscription at index %d not found", index)
			}
			return err
		}
		if expected := parsed.Get("databaseID").Int(); expected != 0 && expected != subID {
			return fmt.Errorf("subscription at index %d changed", index)
		}
		address := parsed.Get("address").String()
		remarks := parsed.Get("remarks").String()
		status := parsed.Get("status").String()
		info := parsed.Get("info").String()
		autoSelect := 0
		if parsed.Get("autoSelect").Bool() {
			autoSelect = 1
		}
		decryptPassword := parsed.Get("decryptPassword").String()
		extraJSON := subExtraFromJSON(b)

		result, err := tx.Exec(
			"UPDATE subscriptions SET address = ?, remarks = ?, status = ?, info = ?, auto_select = ?, decrypt_password = ?, extra_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
			address, remarks, status, info, autoSelect, decryptPassword, extraJSON, subID,
		)
		if err != nil {
			return err
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			return fmt.Errorf("ListSet: subscription at index %d not found", index)
		}

		// Update by sort position to retain stable row IDs and references. All
		// metadata, node writes and removals commit together.
		servers := parsed.Get("servers").Array()
		for j, s := range servers {
			result, err := tx.Exec("UPDATE servers SET config_json = ?, updated_at = CURRENT_TIMESTAMP WHERE type = 'subscription_server' AND sub_id = ? AND sort = ?", s.Raw, subID, j)
			if err != nil {
				return err
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if rows == 0 {
				if _, err = tx.Exec("INSERT INTO servers (type, sub_id, config_json, sort) VALUES ('subscription_server', ?, ?, ?)", subID, s.Raw, j); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec("DELETE FROM servers WHERE type = 'subscription_server' AND sub_id = ? AND sort >= ?", subID, len(servers)); err != nil {
			return err
		}
		return putState(tx, state)
	})
}

func putState(tx *sql.Tx, values map[string]any) error {
	for key, val := range values {
		b, err := jsoniter.Marshal(val)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO system_config (key,value) VALUES (?,?)", key, string(b)); err != nil {
			return err
		}
	}
	return nil
}

func SetState(values map[string]any) error {
	return ReadModifyWrite(func(tx *sql.Tx) error { return putState(tx, values) })
}

// ListGet retrieves an element at a specific index from a list.
func ListGet(bucket string, key string, index int) (b []byte, err error) {
	db := GetDB()

	switch bucket + "/" + key {
	case "touch/servers":
		var configJSON string
		err = db.QueryRow(
			"SELECT config_json FROM servers WHERE type = 'server' AND sort = ?", index,
		).Scan(&configJSON)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("ListGet: can't get element from an empty list")
		}
		if err != nil {
			return nil, err
		}
		return []byte(configJSON), nil

	case "touch/subscriptions":
		var subID int64
		var address, remarks, status, info, decryptPassword, extraJSON string
		var autoSelectInt int
		err = db.QueryRow(
			"SELECT id, address, remarks, status, info, auto_select, decrypt_password, extra_json FROM subscriptions WHERE sort = ?", index,
		).Scan(&subID, &address, &remarks, &status, &info, &autoSelectInt, &decryptPassword, &extraJSON)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("ListGet: can't get element from an empty list")
		}
		if err != nil {
			return nil, err
		}

		// Reconstruct the subscription JSON with servers
		rows, err := db.Query(
			"SELECT config_json FROM servers WHERE type = 'subscription_server' AND sub_id = ? ORDER BY sort",
			subID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var servers []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return nil, err
			}
			servers = append(servers, s)
		}

		serversJSON := "[" + strings.Join(servers, ",") + "]"
		autoSelect := autoSelectInt != 0
		if extraJSON == "" {
			extraJSON = "{}"
		}
		result := subRowJSON{
			DatabaseID:      subID,
			Remarks:         remarks,
			Address:         address,
			Status:          status,
			Info:            info,
			Servers:         jsoniter.RawMessage(serversJSON),
			AutoSelect:      autoSelect,
			DecryptPassword: decryptPassword,
			Extra:           jsoniter.RawMessage(extraJSON),
		}
		b, err := jsoniter.Marshal(result)
		if err != nil {
			return nil, err
		}
		return b, nil

	default:
		return nil, fmt.Errorf("ListGet: unsupported bucket/key: %s/%s", bucket, key)
	}
}

// ListAppend appends values to a list.
func ListAppend(bucket string, key string, val interface{}) (err error) {
	db := GetDB()

	switch bucket + "/" + key {
	case "touch/servers":
		b, err := jsoniter.Marshal(val)
		if err != nil {
			return err
		}
		if !gjson.ParseBytes(b).IsArray() {
			b = append(append([]byte{'['}, b...), ']')
		}
		// One write statement allocates contiguous positions and either appends
		// the entire batch or leaves the node list untouched.
		_, err = db.Exec(`INSERT INTO servers(type, config_json, sort)
			SELECT 'server', value, (SELECT COALESCE(MAX(sort), -1) + 1 FROM servers WHERE type='server') + CAST(key AS INTEGER)
			FROM json_each(?)`, string(b))
		return err

	case "touch/subscriptions":
		b, err := jsoniter.Marshal(val)
		if err != nil {
			return err
		}
		return ReadModifyWrite(func(tx *sql.Tx) error {
			for _, item := range gjson.ParseBytes(b).Array() {
				res, err := tx.Exec(`INSERT INTO subscriptions
					(address, remarks, status, info, auto_select, decrypt_password, extra_json, sort)
					VALUES (?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(sort), -1) + 1 FROM subscriptions))`,
					item.Get("address").String(), item.Get("remarks").String(), item.Get("status").String(),
					item.Get("info").String(), item.Get("autoSelect").Bool(), item.Get("decryptPassword").String(),
					subExtraFromJSON([]byte(item.Raw)))
				if err != nil {
					return err
				}
				subID, err := res.LastInsertId()
				if err != nil {
					return err
				}
				for j, server := range item.Get("servers").Array() {
					if _, err := tx.Exec("INSERT INTO servers(type, sub_id, config_json, sort) VALUES ('subscription_server', ?, ?, ?)",
						subID, server.Raw, j); err != nil {
						return fmt.Errorf("ListAppend: failed to insert subscription server: %w", err)
					}
				}
			}
			return nil
		})

	default:
		b, err := jsoniter.Marshal(val)
		if err != nil {
			return err
		}
		_, err = db.Exec("INSERT OR REPLACE INTO system_config (key, value) VALUES (?, ?)",
			bucket+"/"+key, string(b))
		return err
	}
}

// ListGetAll retrieves all elements from a list.
func ListGetAll(bucket string, key string) (list [][]byte, err error) {
	db := GetDB()

	switch bucket + "/" + key {
	case "touch/servers":
		rows, err := db.Query("SELECT config_json FROM servers WHERE type = 'server' ORDER BY sort")
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			var configJSON string
			if err := rows.Scan(&configJSON); err != nil {
				return nil, err
			}
			list = append(list, []byte(configJSON))
		}
		return list, rows.Err()

	case "touch/subscriptions":
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()

		type subMeta struct {
			id              int64
			address         string
			remarks         string
			status          string
			info            string
			autoSelectInt   int
			decryptPassword string
			extraJSON       string
		}
		rows, err := tx.Query("SELECT id, address, remarks, status, info, auto_select, decrypt_password, extra_json FROM subscriptions ORDER BY sort")
		if err != nil {
			return nil, err
		}
		var metas []subMeta
		for rows.Next() {
			var m subMeta
			if err := rows.Scan(&m.id, &m.address, &m.remarks, &m.status, &m.info, &m.autoSelectInt, &m.decryptPassword, &m.extraJSON); err != nil {
				rows.Close()
				return nil, err
			}
			metas = append(metas, m)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}

		serversBySub := make(map[int64][]string, len(metas))
		if len(metas) > 0 {
			serverRows, err := tx.Query("SELECT sub_id, config_json FROM servers WHERE type = 'subscription_server' ORDER BY sub_id, sort")
			if err != nil {
				return nil, err
			}
			for serverRows.Next() {
				var subID int64
				var s string
				if err := serverRows.Scan(&subID, &s); err != nil {
					serverRows.Close()
					return nil, err
				}
				serversBySub[subID] = append(serversBySub[subID], s)
			}
			if err := serverRows.Err(); err != nil {
				serverRows.Close()
				return nil, err
			}
			if err := serverRows.Close(); err != nil {
				return nil, err
			}
		}

		for _, m := range metas {
			serversJSON := "[" + strings.Join(serversBySub[m.id], ",") + "]"
			autoSelect := m.autoSelectInt != 0
			extraJSON := m.extraJSON
			if extraJSON == "" {
				extraJSON = "{}"
			}
			result := subRowJSON{
				DatabaseID:      m.id,
				Remarks:         m.remarks,
				Address:         m.address,
				Status:          m.status,
				Info:            m.info,
				Servers:         jsoniter.RawMessage(serversJSON),
				AutoSelect:      autoSelect,
				DecryptPassword: m.decryptPassword,
				Extra:           jsoniter.RawMessage(extraJSON),
			}
			b, err := jsoniter.Marshal(result)
			if err != nil {
				return nil, err
			}
			list = append(list, b)
		}
		return list, nil

	default:
		return nil, fmt.Errorf("ListGetAll: unsupported bucket/key: %s/%s", bucket, key)
	}
}

func listRemoveTx(tx *sql.Tx, bucket, key string, indexes []int) error {
	if len(indexes) == 0 {
		return nil
	}
	switch bucket + "/" + key {
	case "touch/servers":
		for _, idx := range indexes {
			if _, err := tx.Exec("DELETE FROM servers WHERE type = 'server' AND sort = ?", idx); err != nil {
				return err
			}
		}
		// Reorder remaining servers
		_, err := tx.Exec(`
			WITH positions AS MATERIALIZED (
				SELECT id, ROW_NUMBER() OVER (ORDER BY sort, id) - 1 AS new_sort
				FROM servers WHERE type = 'server'
			)
			UPDATE servers SET sort = positions.new_sort
			FROM positions WHERE servers.id = positions.id
		`)
		return err

	case "touch/subscriptions":
		for _, idx := range indexes {
			var subID int64
			err := tx.QueryRow("SELECT id FROM subscriptions WHERE sort = ?", idx).Scan(&subID)
			if err != nil {
				if err == sql.ErrNoRows {
					continue
				}
				return err
			}
			if _, err := tx.Exec("DELETE FROM servers WHERE type = 'subscription_server' AND sub_id = ?", subID); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM subscriptions WHERE id = ?", subID); err != nil {
				return err
			}
		}
		// Reorder remaining subscriptions
		_, err := tx.Exec(`
			WITH positions AS MATERIALIZED (
				SELECT id, ROW_NUMBER() OVER (ORDER BY sort, id) - 1 AS new_sort FROM subscriptions
			)
			UPDATE subscriptions SET sort = positions.new_sort
			FROM positions WHERE subscriptions.id = positions.id
		`)
		return err

	default:
		return fmt.Errorf("ListRemove: unsupported bucket/key: %s/%s", bucket, key)
	}
}

// ListRemove removes elements at specified indexes from a list atomically.
func ListRemove(bucket, key string, indexes []int) error {
	if len(indexes) == 0 {
		return fmt.Errorf("ListRemove: nothing to remove")
	}
	switch bucket + "/" + key {
	case "touch/servers", "touch/subscriptions":
		return ReadModifyWrite(func(tx *sql.Tx) error {
			return listRemoveTx(tx, bucket, key, indexes)
		})
	default:
		return fmt.Errorf("ListRemove: unsupported bucket/key: %s/%s", bucket, key)
	}
}

// RemoveTouchAndSetState removes the specified subscription and server indexes
// and writes any updated system_config entries within a single transaction.
func RemoveTouchAndSetState(subscriptionIndexes, serverIndexes []int, state map[string]any) error {
	return ReadModifyWrite(func(tx *sql.Tx) error {
		if len(subscriptionIndexes) > 0 {
			if err := listRemoveTx(tx, "touch", "subscriptions", subscriptionIndexes); err != nil {
				return err
			}
		}
		if len(serverIndexes) > 0 {
			if err := listRemoveTx(tx, "touch", "servers", serverIndexes); err != nil {
				return err
			}
		}
		return putState(tx, state)
	})
}

// ListLen returns the length of a list.
func ListLen(bucket string, key string) (length int, err error) {
	db := GetDB()

	switch bucket + "/" + key {
	case "touch/servers":
		err = db.QueryRow("SELECT COUNT(*) FROM servers WHERE type = 'server'").Scan(&length)
		return length, err

	case "touch/subscriptions":
		err = db.QueryRow("SELECT COUNT(*) FROM subscriptions").Scan(&length)
		return length, err

	default:
		return 0, fmt.Errorf("ListLen: unsupported bucket/key: %s/%s", bucket, key)
	}
}
