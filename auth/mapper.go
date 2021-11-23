package auth

import (
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/venus-auth/storage"
)

type Mapper interface {
	ToOutPutUser(user *storage.User) *OutputUser
	ToOutPutUsers(arr []*storage.User) []*OutputUser
}

type mapper struct {
}

func newMapper() Mapper {

	return &mapper{}
}
func (o *mapper) ToOutPutUser(m *storage.User) *OutputUser {
	if m == nil {
		return nil
	}
	addr, _ := address.NewFromString(m.Miner)
	return &OutputUser{
		Id:         m.Id,
		Name:       m.Name,
		Miner:      addr,
		Comment:    m.Comment,
		State:      m.State,
		IsDeleted:  m.IsDeleted,
		SourceType: m.SourceType,
		CreateTime: m.CreateTime.Unix(),
		UpdateTime: m.UpdateTime.Unix()}
}

func (o *mapper) ToOutPutUsers(arr []*storage.User) []*OutputUser {
	list := make([]*OutputUser, 0, len(arr))
	for _, v := range arr {
		list = append(list, o.ToOutPutUser(v))
	}
	return list
}
