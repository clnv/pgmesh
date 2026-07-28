# Add cache-aside behavior to a query group

Every generated `store: XXX` group has a matching
`WithXXXFactory(func(XXX) XXX)` store option. Use it to wrap only the groups
that need application-owned behavior such as cache-aside reads and cache
invalidation on writes.

For a `store: Users` group, embed the generated `Users` interface so methods
that do not need custom behavior continue to delegate to pgmesh:

```go
type cachedUsers struct {
    db.Users
    cache *UserCache
}

func (s *cachedUsers) GetUser(
    ctx context.Context,
    arg *db.GetUserT,
    options ...db.QueryOption,
) (*db.User, error) {
    // Query options may request a primary or transaction route. Bypassing the
    // cache whenever options are present preserves those routing semantics.
    if len(options) != 0 {
        return s.Users.GetUser(ctx, arg, options...)
    }

    if user, ok := s.cache.Get(arg.TenantID, arg.ID); ok {
        return user, nil
    }
    user, err := s.Users.GetUser(ctx, arg)
    if err == nil {
        s.cache.Put(user)
    }
    return user, err
}
```

Override generated write methods in the same wrapper when they must update or
invalidate cached values. Cache keys, expiration, stampede prevention, error
handling, and consistency policy remain application responsibilities.

Register the wrapper while constructing the root store:

```go
store, err := db.NewStore(
    ctx,
    db.Singleton(pool),
    db.WithUsersFactory(func(internalStore db.Users) db.Users {
        return &cachedUsers{
            Users: internalStore,
            cache: userCache,
        }
    }),
)
```

The factory runs once after the topology has built successfully, and repeated
`store.Users()` calls return the same wrapper. Other query groups remain
unwrapped unless their own factory option is supplied.

Factories are synchronous and do not return errors. Generated code uses the
returned interface as-is, so a factory must return a non-nil implementation.
When the same group option appears more than once, the last value wins.
`WithUsersFactory(nil)` therefore disables an earlier Users factory.

The wrapper receives the normal generated interface rather than pgmesh's
private routing implementation. Delegated calls retain replica, shard,
transaction, mirror, telemetry, and logging behavior.
