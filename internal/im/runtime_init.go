package im

type RuntimeInitOptions struct {
	Workspace        string
	EnabledAdapters  map[string]bool
	OnUpdate         func(StatusSnapshot)
	RegisterInstance bool
}

type RuntimeInitResult struct {
	Manager        *Manager
	BindingStore   BindingStore
	PairingStore   PairingStateStore
	InstanceDetect *InstanceDetect
	OtherInstances []InstanceInfo
}

func InitRuntime(opts RuntimeInitOptions) (*RuntimeInitResult, error) {
	mgr := NewManager()

	bindingsPath, err := DefaultBindingsPath()
	if err != nil {
		return nil, err
	}
	bindingStore, err := NewJSONFileBindingStore(bindingsPath)
	if err != nil {
		return nil, err
	}
	if err := mgr.SetBindingStore(bindingStore); err != nil {
		return nil, err
	}

	pairingPath, err := DefaultPairingStatePath()
	if err != nil {
		return nil, err
	}
	pairingStore, err := NewJSONFilePairingStore(pairingPath)
	if err != nil {
		return nil, err
	}
	if err := mgr.SetPairingStore(pairingStore); err != nil {
		return nil, err
	}

	mgr.BindSession(SessionBinding{Workspace: opts.Workspace})
	if len(opts.EnabledAdapters) > 0 {
		mgr.ApplyAdapterConfig(opts.EnabledAdapters)
	}
	if opts.OnUpdate != nil {
		mgr.SetOnUpdate(opts.OnUpdate)
	}

	result := &RuntimeInitResult{
		Manager:      mgr,
		BindingStore: bindingStore,
		PairingStore: pairingStore,
	}

	// Start the binding watcher so we detect when another instance claims
	// a binding that we currently own.
	mgr.StartBindingWatcher()

	if opts.RegisterInstance && opts.Workspace != "" {
		sid := ""
		if mgr.session != nil {
			sid = mgr.session.SessionID
		}
		detect, others, err := mgr.RegisterInstance(opts.Workspace, sid)
		if err != nil {
			return nil, err
		}
		result.InstanceDetect = detect
		result.OtherInstances = others
	}
	return result, nil
}
