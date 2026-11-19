import { describe, expect, it } from 'vitest'
import { matchPath } from './router'

describe('matchPath', () => {
  it('matches static paths and ignores empty segments', () => {
    expect(matchPath('/overview', '/overview')).toEqual({})
    expect(matchPath('/overview', '//overview/')).toEqual({})
    expect(matchPath('/overview', '/overview/extra')).toBeNull()
    expect(matchPath('/overview', '/other')).toBeNull()
  })

  it('captures named parameters', () => {
    expect(matchPath('/connectors/:id', '/connectors/abc-123')).toEqual({ id: 'abc-123' })
    expect(matchPath('/integrations/:id/resources/:rid', '/integrations/i1/resources/r2')).toEqual({ id: 'i1', rid: 'r2' })
    expect(matchPath('/connectors/:id', '/connectors')).toBeNull()
  })

  it('decodes URI components in parameters', () => {
    expect(matchPath('/targets/:name', '/targets/a%20b')).toEqual({ name: 'a b' })
    expect(matchPath('/targets/:name', '/targets/%E4%B8%AD')).toEqual({ name: '中' })
  })

  it('rejects malformed percent-encoding instead of throwing', () => {
    expect(matchPath('/targets/:name', '/targets/%zz')).toBeNull()
  })

  it('wildcard matches any single-segment shape used by the router', () => {
    expect(matchPath('*', '/anything')).toEqual({})
  })
})