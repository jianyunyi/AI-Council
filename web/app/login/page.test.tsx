// @vitest-environment jsdom
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'

const signIn=vi.fn()
const push=vi.fn()
vi.mock('next/navigation',()=>({useRouter:()=>({push})}))
vi.mock('@/components/auth-session',()=>({useAuth:()=>({status:'anonymous',signIn})}))
import LoginPage from './page'

afterEach(()=>{cleanup();vi.clearAllMocks()})

describe('LoginPage',()=>{
 it('submits credentials and redirects to the account overview',async()=>{
  signIn.mockResolvedValue(undefined)
  render(<LoginPage/> )
  fireEvent.change(screen.getByLabelText('账号'),{target:{value:'admin'}})
  fireEvent.change(screen.getByLabelText('密码'),{target:{value:'correct horse'}})
  fireEvent.click(screen.getByRole('button',{name:'登录'}))
  await waitFor(()=>expect(signIn).toHaveBeenCalledWith('admin','correct horse'))
  expect(push).toHaveBeenCalledWith('/account')
 })

 it('keeps the password out of the error message when login fails',async()=>{
  signIn.mockRejectedValue(new Error('invalid credentials'))
  render(<LoginPage/> )
  fireEvent.change(screen.getByLabelText('账号'),{target:{value:'admin'}})
  fireEvent.change(screen.getByLabelText('密码'),{target:{value:'top-secret'}})
  fireEvent.click(screen.getByRole('button',{name:'登录'}))
  expect((await screen.findByRole('alert')).textContent).toContain('invalid credentials')
  expect(screen.getByRole('alert').textContent).not.toContain('top-secret')
 })
})
