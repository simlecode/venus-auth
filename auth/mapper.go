package auth

import "github.com/filecoin-project/venus-auth/storage"

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
	return &OutputUser{
		Id:         m.Id,
		Name:       m.Name,
		Miner:      m.Miner,
		Comment:    m.Comment,
		State:      m.State,
		SourceType: m.SourceType,
		CreateTime: m.CreateTime.Unix(),
		UpdateTime: m.UpdateTime.Unix(),
		Burst:      m.Burst,
		Rate:       m.Rate}
}

func (o *mapper) ToOutPutUsers(arr []*storage.User) []*OutputUser {
	list := make([]*OutputUser, 0, len(arr))
	for _, v := range arr {
		list = append(list, o.ToOutPutUser(v))
	}
	return list
}
