import {afterEach,describe,expect,it,vi} from 'vitest'
import {apiBase,authorizationHeader,getDesktopRuntime} from './desktop'

describe('desktop runtime bridge',()=>{
  afterEach(()=>{
    vi.unstubAllGlobals()
  })

  it('uses the injected local session only when both fields are present',()=>{
    vi.stubGlobal('window',{__AI_COUNCIL_DESKTOP__:{apiBase:'http://127.0.0.1:19000/api/v1',sessionToken:'ephemeral-token'}})
    expect(getDesktopRuntime()).toEqual({apiBase:'http://127.0.0.1:19000/api/v1',sessionToken:'ephemeral-token'})
    expect(apiBase()).toBe('http://127.0.0.1:19000/api/v1')
    expect(authorizationHeader()).toEqual({authorization:'Bearer ephemeral-token'})
  })
})
