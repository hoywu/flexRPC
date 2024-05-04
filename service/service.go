package service

import (
	"reflect"
)

type Service struct {
	Name     string
	Methods  map[string]*Method
	receiver reflect.Value
}

type Method struct {
	method reflect.Method
	Arg    reflect.Type
	Reply  reflect.Type
}

// 调用方法，arg 和 reply 的 Kind 必须是 Pointer
func (s *Service) Call(m *Method, arg, reply reflect.Value) error {
	// 检查 arg 和 reply 的类型
	if m.Arg.Kind() != reflect.Pointer {
		arg = arg.Elem()
	}
	switch m.Reply.Elem().Kind() {
	case reflect.Map:
		reply.Elem().Set(reflect.MakeMap(m.Reply.Elem()))
	case reflect.Slice:
		reply.Elem().Set(reflect.MakeSlice(m.Reply.Elem(), 0, 0))
	}

	// 调用方法
	f := m.method.Func
	returnValues := f.Call([]reflect.Value{s.receiver, arg, reply})

	// 检查返回值
	err := returnValues[0].Interface()
	if err != nil {
		return err.(error)
	}
	return nil
}

// name 为空时使用 receiver 的类型名
func NewService(receiver interface{}, name string) *Service {
	s := &Service{
		Name:     name,
		receiver: reflect.ValueOf(receiver),
		Methods:  make(map[string]*Method),
	}

	defaultName := reflect.Indirect(s.receiver).Type().Name()
	if s.Name == "" {
		s.Name = defaultName
	}

	s.registerMethods()
	return s
}

func (s *Service) registerMethods() {
	typ := s.receiver.Type()
	// 遍历 receiver 导出的方法
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		mType := m.Type
		mName := m.Name

		// 只注册接收 2 个参数 (第一个参数是 receiver)
		// 返回 1 个 error 的方法
		if mType.NumIn() != 3 || mType.NumOut() != 1 {
			continue
		}
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}

		argType, replyType := mType.In(1), mType.In(2)
		if replyType.Kind() != reflect.Pointer {
			// reply 必须是指针类型
			continue
		}

		s.Methods[mName] = &Method{
			method: m,
			Arg:    argType,
			Reply:  replyType,
		}
	}
}
