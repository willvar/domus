import axios from 'axios'
import type { AxiosInstance, AxiosResponse, AxiosError } from 'axios'
import axiosRetry from 'axios-retry'

const API_BASE: string = import.meta.env.VITE_API_BASE || ''

const api: AxiosInstance = axios.create({
  baseURL: API_BASE,
  timeout: 30000,
  withCredentials: true,
})

axiosRetry(api, {
  retries: 1,
  retryDelay: axiosRetry.exponentialDelay,
  retryCondition: (error: AxiosError): boolean => !error.response && !axios.isCancel(error),
})

api.interceptors.response.use(
  (res: AxiosResponse): AxiosResponse => res,
  (error: AxiosError): Promise<never> => {
    if (error.response?.status === 401) {
      window.dispatchEvent(new Event('auth:expired'))
    }
    return Promise.reject(error)
  }
)

export function useApi(): AxiosInstance {
  return api
}

export default api
