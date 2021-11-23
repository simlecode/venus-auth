package storage

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/dgraph-io/badger/v3"
	"github.com/filecoin-project/go-address"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/venus-auth/core"
)

var _ Store = &badgerStore{}

type badgerStore struct {
	db *badger.DB
}

func newBadgerStore(filePath string) (Store, error) {
	db, err := badger.Open(badger.DefaultOptions(filePath))
	if err != nil {
		return nil, xerrors.Errorf("open db failed :%s", err)
	}
	s := &badgerStore{
		db: db,
	}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
		again:
			err := db.RunValueLogGC(0.7)
			if err == nil {
				goto again
			}
		}
	}()
	return s, nil
}

func (s *badgerStore) Put(kp *KeyPair) error {
	val, err := kp.Bytes()
	if err != nil {
		return xerrors.Errorf("failed to marshal time :%s", err)
	}
	key := s.tokenKey(kp.Token.String())
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, val)
	})
}

func (s *badgerStore) Delete(token Token) error {
	kp, err := s.Get(token)
	if err != nil {
		return err
	}
	kp.IsDeleted = core.Deleted

	return s.Put(kp)
}

func (s *badgerStore) Has(token Token) (bool, error) {
	key := s.tokenKey(token.String())
	var value []byte
	var has bool
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		if value, err = item.ValueCopy(nil); err != nil {
			return err
		}
		kp := new(KeyPair)
		if err := kp.FromBytes(value); err != nil {
			return err
		}
		if !kp.IsDeleted {
			has = true
		}

		return nil
	})
	if err != nil {
		if err.Error() == "Key not found" {
			return false, nil
		}
		return false, err
	}
	return has, nil
}

func (s *badgerStore) Get(token Token) (*KeyPair, error) {
	kp := new(KeyPair)
	key := s.tokenKey(token.String())

	err := s.db.View(func(txn *badger.Txn) error {
		val, err := txn.Get(key)
		if err != nil || err == badger.ErrKeyNotFound {
			return xerrors.Errorf("token %s not exit", token)
		}

		return val.Value(func(val []byte) error {
			return kp.FromBytes(val)
		})
	})
	if kp.IsDeleted {
		return nil, xerrors.Errorf("token %s is deleted", token.String())
	}

	return kp, err
}

func (s *badgerStore) ByName(name string) ([]*KeyPair, error) {
	res := make([]*KeyPair, 0)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.IteratorOptions{
			PrefetchValues: true,
			Reverse:        false,
			AllVersions:    false,
			Prefix:         []byte(PrefixToken),
		}
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			val := new([]byte)
			err := item.Value(func(v []byte) error {
				val = &v
				return nil
			})
			if err != nil {
				return err
			}
			kp := new(KeyPair)
			err = kp.FromBytes(*val)
			if err != nil {
				return err
			}
			if kp.IsDeleted {
				continue
			}
			if kp.Name == name {
				res = append(res, kp)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *badgerStore) UpdateToken(kp *KeyPair) error {
	val, err := kp.Bytes()
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(s.tokenKey(kp.Token.String()), val)
	})
}

func (s *badgerStore) List(skip, limit int64) ([]*KeyPair, error) {
	data := make(chan *KeyPair, limit)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.IteratorOptions{
			PrefetchValues: true,
			Reverse:        false,
			AllVersions:    false,
			Prefix:         []byte(PrefixToken),
		}
		it := txn.NewIterator(opts)
		defer it.Close()
		idx := int64(0)
		for it.Rewind(); it.Valid() && idx < skip+limit; it.Next() {
			if idx >= skip {
				item := it.Item()
				val := new([]byte)
				err := item.Value(func(v []byte) error {
					val = &v
					return nil
				})
				if err != nil {
					close(data)
					return err
				}
				kp := new(KeyPair)
				err = kp.FromBytes(*val)
				if err != nil {
					close(data)
					return err
				}
				if kp.IsDeleted {
					continue
				}
				data <- kp
			}
			idx++
		}
		close(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	res := make([]*KeyPair, 0, limit)
	for ch := range data {
		res = append(res, ch)
	}
	return res, nil
}

func (s *badgerStore) GetUser(name string) (*User, error) {
	user := new(User)
	err := s.db.View(func(txn *badger.Txn) error {
		val, err := txn.Get(s.userKey(name))
		if err != nil || err == badger.ErrKeyNotFound {
			return xerrors.Errorf("users %s not exit", name)
		}

		return val.Value(func(val []byte) error {
			return user.FromBytes(val)
		})
	})
	if err != nil {
		return nil, err
	}
	if user.IsDeleted {
		return nil, xerrors.Errorf("user %s is deleted", name)
	}
	return user, nil
}

func (s *badgerStore) UpdateUser(user *User) error {
	old, err := s.GetUser(user.Name)
	if err != nil {
		return err
	}
	user.Id = old.Id
	val, err := user.Bytes()
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(s.userKey(user.Name), val)
	})
}

// If `skipIsDeleted` is true, whether the user is deleted is ignored
func (s *badgerStore) hasUser(name string, skipIsDeleted bool) (bool, error) {
	var value []byte
	var has bool
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(s.userKey(name))
		if err != nil {
			return err
		}
		if value, err = item.ValueCopy(nil); err != nil {
			return err
		}
		user := new(User)
		if err := user.FromBytes(value); err != nil {
			return err
		}
		if !user.IsDeleted {
			has = true
		}
		if skipIsDeleted {
			has = true
		}
		return nil
	})
	if err != nil {
		if err.Error() == "Key not found" {
			return false, nil
		}
		return false, err
	}
	return has, nil
}

func (s *badgerStore) HasUser(name string) (bool, error) {
	return s.hasUser(name, false)
}

// Only used to create new user
func (s *badgerStore) PutUser(user *User) error {
	has, err := s.hasUser(user.Name, true)
	if err != nil {
		return err
	}
	// Do not allow the newly created user to overwrite the deleted user
	if has {
		return xerrors.Errorf("found a record")
	}
	val, err := user.Bytes()
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(s.userKey(user.Name), val)
	})
}

func (s *badgerStore) ListUsers(skip, limit int64, state int, sourceType core.SourceType, code core.KeyCode) ([]*User, error) {
	var data []*User
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.IteratorOptions{
			PrefetchValues: true,
			Reverse:        false,
			AllVersions:    false,
			Prefix:         []byte(PrefixUser),
		}
		it := txn.NewIterator(opts)
		defer it.Close()
		idx := int64(0)
		for it.Rewind(); it.Valid() && idx < skip+limit; it.Next() {
			if idx >= skip {
				item := it.Item()
				// k := item.Key()
				val := new([]byte)
				err := item.Value(func(v []byte) error {
					val = &v
					return nil
				})
				if err != nil {
					return err
				}
				user := new(User)
				err = user.FromBytes(*val)
				if err != nil {
					return err
				}
				if user.IsDeleted {
					continue
				}
				// aggregation multi-select
				need := false
				if code&1 == 1 {
					need = user.SourceType == sourceType
				} else {
					need = true
				}

				if !need {
					continue
				}
				if code&2 == 2 {
					need = need && user.State == state
				} else {
					need = need && true
				}
				if need {

					data = append(data, user)
					idx++
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *badgerStore) DeleteUser(name string) error {
	user, err := s.GetUser(name)
	if err != nil {
		return err
	}
	user.IsDeleted = core.Deleted

	return s.UpdateUser(user)
}

func (s *badgerStore) HasMiner(maddr address.Address) (bool, error) {
	var has bool
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.IteratorOptions{
			PrefetchValues: true,
			Reverse:        false,
			AllVersions:    false,
			Prefix:         []byte(PrefixUser),
		}
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			// k := item.Key()
			val := new([]byte)
			err := item.Value(func(v []byte) error {
				// fmt.Printf("key=%s, value=%s\n", k, v)
				val = &v
				return nil
			})
			if err != nil {
				return err
			}
			user := new(User)
			err = user.FromBytes(*val)
			if err != nil {
				return err
			}
			if user.IsDeleted {
				continue
			}

			if user.Miner == maddr.String() {
				has = true
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return has, nil
}

func (s *badgerStore) GetMiner(maddr address.Address) (*User, error) {
	var data *User
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.IteratorOptions{
			PrefetchValues: true,
			Reverse:        false,
			AllVersions:    false,
			Prefix:         []byte(PrefixUser),
		}
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			// k := item.Key()
			val := new([]byte)
			err := item.Value(func(v []byte) error {
				// fmt.Printf("key=%s, value=%s\n", k, v)
				val = &v
				return nil
			})
			if err != nil {
				return err
			}
			user := new(User)
			err = user.FromBytes(*val)
			if err != nil {
				return err
			}
			if user.IsDeleted {
				continue
			}
			if user.Miner == maddr.String() {
				data = user
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, xerrors.Errorf("miner %s not exit", maddr)
	}
	return data, nil
}

func (s *badgerStore) GetRateLimits(name, id string) ([]*UserRateLimit, error) {
	mRateLimits, err := s.listRateLimits(name, id)
	if err != nil {
		return nil, err
	}

	var rateLimits = make([]*UserRateLimit, len(mRateLimits))
	var idx = 0
	for _, l := range mRateLimits {
		rateLimits[idx] = l
		idx++
	}

	return rateLimits, err
}

func (s *badgerStore) PutRateLimit(limit *UserRateLimit) (string, error) {
	if limit.Id == "" {
		limit.Id = uuid.NewString()
	}
	limits, err := s.listRateLimits(limit.Name, "")
	if err != nil {
		if !xerrors.Is(err, badger.ErrKeyNotFound) {
			return "", err
		}
		limits = make(map[string]*UserRateLimit)
	}

	limits[limit.Id] = limit

	return limit.Id, s.updateUserRateLimit(limit.Name, limits)
}

func (s *badgerStore) DelRateLimit(name, id string) error {
	if len(name) == 0 || len(id) == 0 {
		return errors.New("user and rate-limit id is required for removing rate limit regulation")
	}

	mRateLimit, err := s.listRateLimits(name, id)
	if err != nil {
		return err
	}
	if _, exist := mRateLimit[id]; !exist {
		return nil
	}
	delete(mRateLimit, id)
	return s.updateUserRateLimit(name, mRateLimit)
}

func (s *badgerStore) listRateLimits(user, id string) (map[string]*UserRateLimit, error) {
	var mRateLimits map[string]*UserRateLimit
	if err := s.db.View(func(txn *badger.Txn) error {
		val, err := txn.Get(s.rateLimitKey(user))
		if err != nil || err == badger.ErrKeyNotFound {
			return xerrors.Errorf("users %s not exit, %w", user, err)
		}
		return val.Value(func(val []byte) error {
			return json.Unmarshal(val, &mRateLimits)
		})
	}); err != nil {
		return nil, err
	}

	if len(id) != 0 {
		res := make(map[string]*UserRateLimit)
		if rl, exists := mRateLimits[id]; exists {
			res[id] = rl
		}
		return res, nil
	}

	return mRateLimits, nil

}

func (s *badgerStore) updateUserRateLimit(name string, limits map[string]*UserRateLimit) error {
	val, err := json.Marshal(limits)
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(s.rateLimitKey(name), val)
	})
}
